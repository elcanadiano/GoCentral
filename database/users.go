package database

import (
	"context"
	"regexp"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// UserSearchResult is a minimal user identity for REST search / future detail endpoints.
type UserSearchResult struct {
	PID         int    `bson:"pid" json:"pid"`
	Username    string `bson:"username" json:"username"`
	ConsoleType int    `bson:"console_type" json:"console_type"`
}

const userSearchDefaultLimit = 5

// FindUsersByUsernamePrefix returns up to limit users whose username starts with q
// (case-insensitive autocomplete). q should already be validated by the caller.
func FindUsersByUsernamePrefix(ctx context.Context, db *mongo.Database, q string, limit int64) ([]UserSearchResult, error) {
	if limit < 1 {
		limit = userSearchDefaultLimit
	}

	filter := bson.M{
		"username": bson.M{
			"$regex":   "^" + regexp.QuoteMeta(q),
			"$options": "i",
		},
	}

	opts := options.Find().
		SetProjection(bson.M{"pid": 1, "username": 1, "console_type": 1, "_id": 0}).
		SetSort(bson.D{{Key: "username", Value: 1}}).
		SetLimit(limit)

	cursor, err := db.Collection("users").Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var results []UserSearchResult
	if err := cursor.All(ctx, &results); err != nil {
		return nil, err
	}
	if results == nil {
		results = []UserSearchResult{}
	}
	return results, nil
}
