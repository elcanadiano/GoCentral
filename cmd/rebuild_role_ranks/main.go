package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"rb3server/database"
)

func main() {
	envFile := flag.String("env", ".env", "specify the .env file to load")
	flag.Parse()

	if err := godotenv.Load(*envFile); err != nil {
		log.Println("Error loading .env file, using environment variables instead")
	}

	uri := os.Getenv("MONGOCONNECTIONSTRING")
	if uri == "" {
		log.Fatalln("MONGOCONNECTIONSTRING is required")
	}

	mongoDatabase := os.Getenv("MONGODATABASE")
	if mongoDatabase == "" {
		mongoDatabase = "gocentral"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		log.Fatalln("Could not connect to MongoDB:", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = client.Disconnect(shutdownCtx)
	}()

	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		log.Fatalln("Could not ping MongoDB:", err)
	}

	db := client.Database(mongoDatabase)
	log.Printf("Rebuilding role_ranks from scores in database %q...", mongoDatabase)

	n, err := database.RebuildRoleRanks(ctx, db)
	if err != nil {
		log.Fatalln("RebuildRoleRanks failed:", err)
	}

	log.Printf("Rebuilt %d role_ranks documents", n)
}
