package database

import (
	"context"
	"regexp"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// UserSearchResult is a minimal user identity for REST search / role-ranks detail.
// Username is console-prefixed (e.g. "elcanadiano [RPCS3]").
type UserSearchResult struct {
	PID      int    `json:"pid"`
	Username string `json:"username"`
}

type userIdentityRow struct {
	PID         int    `bson:"pid"`
	Username    string `bson:"username"`
	ConsoleType int    `bson:"console_type"`
}

const userSearchDefaultLimit = 5

func userSearchResultFromRow(row userIdentityRow) UserSearchResult {
	return UserSearchResult{
		PID:      row.PID,
		Username: FormatConsolePrefixedUsername(row.Username, row.ConsoleType),
	}
}

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

	var rows []userIdentityRow
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, err
	}

	results := make([]UserSearchResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, userSearchResultFromRow(row))
	}
	return results, nil
}

// GetUserSearchResultByPID returns the minimal user identity for pid, or false if missing.
func GetUserSearchResultByPID(ctx context.Context, db *mongo.Database, pid int) (UserSearchResult, bool, error) {
	var row userIdentityRow
	err := db.Collection("users").FindOne(
		ctx,
		bson.M{"pid": pid},
		options.FindOne().SetProjection(bson.M{"pid": 1, "username": 1, "console_type": 1, "_id": 0}),
	).Decode(&row)
	if err == mongo.ErrNoDocuments {
		return UserSearchResult{}, false, nil
	}
	if err != nil {
		return UserSearchResult{}, false, err
	}
	return userSearchResultFromRow(row), true, nil
}
