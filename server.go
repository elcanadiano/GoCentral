package main

import (
	"context"
	"flag"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	"github.com/natefinch/lumberjack"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	database "rb3server/database"
	"rb3server/restapi"
	"rb3server/servers"
)

func main() {

	envFile := flag.String("env", ".env", "specify the .env file to load")
	flag.Parse()

	err := godotenv.Load(*envFile)
	if err != nil {
		log.Println("Error loading .env file, using environment variables instead")
	}

	// if the user has set a log path, log there, otherwise log to stdout
	logPath := os.Getenv("LOGPATH")

	if logPath != "" {
		log.SetOutput(&lumberjack.Logger{
			Filename:   logPath,
			MaxSize:    10,   // Max size in MB before rotation
			MaxBackups: 3,    // Max number of old log files to retain
			MaxAge:     28,   // Max number of days to retain old log files
			Compress:   true, // Compress/zip old log files
		})
	}

	ticketVerifierEndpoint := os.Getenv("TICKETVERIFIERENDPOINT")

	if ticketVerifierEndpoint == "" {
		log.Println("Ticket verification is disabled, GoCentral will have no real authentication! Please do not use this server in a production environment.")
	}

	uri := os.Getenv("MONGOCONNECTIONSTRING")

	if uri == "" {
		log.Fatalln("GoCentral relies on MongoDB. You must set a MongoDB connection string to use GoCentral")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))

	if err != nil {
		log.Fatalln("Could not connect to MongoDB: ", err)
	}

	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err = client.Disconnect(shutdownCtx); err != nil {
			log.Fatalln("Could not connect to MongoDB: ", err)
		}
	}()

	// Ping the primary
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		log.Fatalln("Could not ping MongoDB: ", err)
	}

	log.Println("Successfully established connection to MongoDB")

	mongoDatabase := os.Getenv("MONGODATABASE")
	if mongoDatabase == "" {
		mongoDatabase = "gocentral"
	}

	database.GocentralDatabase = client.Database(mongoDatabase)
	log.Printf("Using MongoDB database %q", mongoDatabase)

	configCollection := database.GocentralDatabase.Collection("config")

	// get config from DB
	err = configCollection.FindOne(nil, bson.M{}).Decode(&servers.Config)
	if err != nil {
		log.Println("Could not get config from MongoDB database, creating default config: ", err)
		_, err = configCollection.InsertOne(nil, bson.D{
			{Key: "last_pid", Value: 500},
			{Key: "last_band_id", Value: 0},
			{Key: "last_character_id", Value: 0},
			{Key: "last_setlist_id", Value: 0},
			{Key: "profanity_list", Value: []string{}},
			{Key: "battle_limit", Value: 5},
			{Key: "last_machine_id", Value: 1000000000},
		})

		servers.Config.LastPID = 500
		servers.Config.LastCharacterID = 0
		servers.Config.LastBandID = 0
		servers.Config.LastSetlistID = 0
		servers.Config.ProfanityList = []string{}
		servers.Config.BattleLimit = 1

		if err != nil {
			log.Fatalln("Could not create default config! GoCentral cannot proceed: ", err)
		}
	}

	// seed randomness with current time
	rand.Seed(time.Now().UnixNano())

	// Initialize the in-memory message store
	servers.InitMessageStore()

	go servers.StartAuthServer()
	go servers.StartSecureServer()

	if database.UseMaterializedRoleRanks() {
		log.Println("Materialized role_ranks enabled for jsonproto Total/RB3 boards (USE_MATERIALIZED_ROLE_RANKS)")
	}

	if envTrue(os.Getenv("DEBUGNETWORK")) {
		// only enable for secure server now since that's the one that has the most complex network interactions, and the auth server is pretty straightforward
		// TODO: have a way to enable debug network for the auth server as well, cba right now
		servers.SecureServer.SetDebugNetwork(true)
	}

	// Start HTTP server using Chi
	enableRESTAPI := envTrue(os.Getenv("ENABLERESTAPI"))

	httpServer := &http.Server{}

	if enableRESTAPI {
		r := chi.NewRouter()
		r.Use(middleware.Recoverer)

		// Live REST request latency logging (Chi/HTTP only — not NEX). Off by default.
		if envTrue(os.Getenv("ENABLERESTBENCHMARK")) {
			r.Use(restapi.RequestTimingMiddleware)
			log.Println("REST request timing middleware enabled (ENABLERESTBENCHMARK)")
		}

		// used to check if the server is up
		r.Get("/health", restapi.HealthHandler)

		// some basic stats about how many chars/bands/scores/etc are in the DB
		// does not include any user-specific information
		r.Get("/stats", restapi.StatsHandler)

		// used to get the current MOTD
		r.Get("/motd", restapi.MotdHandler)

		r.Get("/song_list", restapi.SongListHandler)

		// legacy endpoint, will keep around for now
		r.Get("/leaderboards", restapi.LeaderboardHandler)

		r.Get("/leaderboards/song", restapi.LeaderboardHandler)
		r.Get("/leaderboards/battle", restapi.BattleLeaderboardHandler)
		r.Get("/leaderboards/role-rank", restapi.RoleRankHandler)
		r.Get("/leaderboards/role-rank/legacy", restapi.RoleRankLegacyHandler)

		r.Get("/battles", restapi.BattleListHandler)

		r.Get("/users/search", restapi.UserSearchHandler)
		r.Get("/role-ranks", restapi.PlayerRoleRanksHandler)

		r.Route("/admin", func(r chi.Router) {
			r.Use(restapi.AdminTokenAuth)

			// battle management
			r.Post("/battles/create", restapi.CreateBattleHandler)
			r.Delete("/battles", restapi.DeleteBattleHandler)

			// ban Management
			r.Get("/players/banned", restapi.ListBannedPlayersHandler)
			r.Post("/players/ban", restapi.BanPlayerHandler)
			r.Post("/players/unban", restapi.UnbanPlayerHandler)
			r.Delete("/players/scores", restapi.DeletePlayerScoresHandler)
		})

		httpPort := os.Getenv("HTTPPORT")

		if httpPort == "" {
			log.Printf("REST API enabled but HTTP port, not set, please set an HTTP port using the HTTPPORT environment variable!")
			return
		}

		httpServer = &http.Server{
			Addr:    ":" + httpPort,
			Handler: r,
		}

		go func() {
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("Could not listen on :%v: %v\n", httpPort, err)
			}
		}()
		log.Println("GoCentral REST API HTTP server started on:" + httpPort)
	}

	enableHousekeeping := envTrue(os.Getenv("ENABLEHOUSEKEEPING"))
	housekeepingLeaderValue, housekeepingLeaderSet := os.LookupEnv("HOUSEKEEPING_LEADER")
	shouldRunHousekeeping := enableHousekeeping && (!housekeepingLeaderSet || envTrue(housekeepingLeaderValue))
	quit := make(chan struct{})

	if shouldRunHousekeeping {
		log.Printf("Starting housekeeping tasks...\n")
		go runHousekeepingScheduler(quit, configuredHousekeepingJobs())
	} else if enableHousekeeping {
		log.Println("Housekeeping enabled but skipped on this instance because HOUSEKEEPING_LEADER is false")
	}

	sig := make(chan os.Signal)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	s := <-sig
	log.Printf("Signal (%s) received, stopping\n", s)

	// Stop the message store purge loop
	servers.StopMessageStore()

	if enableRESTAPI {
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			log.Fatalf("HTTP server Shutdown: %v", err)
		}
	}
	close(quit)
}

func envTrue(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
