package database

import (
	"context"
	"fmt"

	"rb3server/models"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const RoleRanksCollectionName = "role_ranks"

// RB3 on-disc song_id range (jsonproto LBTypeRB3Only).
const (
	RB3OnDiscSongIDMin = 1001
	RB3OnDiscSongIDMax = 1106
)

// IsRB3OnDiscSong reports whether songID is in the on-disc RB3 range.
func IsRB3OnDiscSong(songID int) bool {
	return songID >= RB3OnDiscSongIDMin && songID <= RB3OnDiscSongIDMax
}

// EnsureRoleRankIndexes creates unique and leaderboard indexes on role_ranks.
// Safe to call repeatedly (CreateMany is idempotent for existing indexes).
func EnsureRoleRankIndexes(ctx context.Context, db *mongo.Database) error {
	coll := db.Collection(RoleRanksCollectionName)
	_, err := coll.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "pid", Value: 1}, {Key: "role_id", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys: bson.D{{Key: "role_id", Value: 1}, {Key: "total_score", Value: -1}},
		},
		{
			Keys: bson.D{{Key: "role_id", Value: 1}, {Key: "rb3_score", Value: -1}},
		},
	})
	return err
}

// ApplyRoleRankScoreDelta increments materialized totals after a song score improvement.
// delta should be (newScore - oldScore); oldScore is 0 when inserting. No-op when delta == 0.
func ApplyRoleRankScoreDelta(ctx context.Context, db *mongo.Database, pid, roleID, delta int, rb3 bool) error {
	if delta == 0 {
		return nil
	}

	inc := bson.D{{Key: "total_score", Value: delta}}
	if rb3 {
		inc = append(inc, bson.E{Key: "rb3_score", Value: delta})
	}

	_, err := db.Collection(RoleRanksCollectionName).UpdateOne(
		ctx,
		bson.M{"pid": pid, "role_id": roleID},
		bson.D{{Key: "$inc", Value: inc}},
		options.Update().SetUpsert(true),
	)
	return err
}

// GetRoleRank returns the materialized row for (pid, roleID), or false if missing.
func GetRoleRank(ctx context.Context, db *mongo.Database, pid, roleID int) (models.RoleRank, bool, error) {
	var row models.RoleRank
	err := db.Collection(RoleRanksCollectionName).FindOne(ctx, bson.M{"pid": pid, "role_id": roleID}).Decode(&row)
	if err == mongo.ErrNoDocuments {
		return models.RoleRank{}, false, nil
	}
	if err != nil {
		return models.RoleRank{}, false, err
	}
	return row, true, nil
}

// RebuildRoleRanks clears role_ranks, ensures indexes, and repopulates from scores
// using the same battle/setlist exclusions as live role-total aggregation.
// Returns the number of documents written.
func RebuildRoleRanks(ctx context.Context, db *mongo.Database) (int, error) {
	coll := db.Collection(RoleRanksCollectionName)

	if _, err := coll.DeleteMany(ctx, bson.M{}); err != nil {
		return 0, fmt.Errorf("clear role_ranks: %w", err)
	}

	if err := EnsureRoleRankIndexes(ctx, db); err != nil {
		return 0, fmt.Errorf("ensure role_ranks indexes: %w", err)
	}

	match := bson.D{
		{Key: "battle_id", Value: bson.D{{Key: "$not", Value: bson.D{{Key: "$gt", Value: 0}}}}},
		{Key: "setlist_id", Value: bson.D{{Key: "$not", Value: bson.D{{Key: "$gt", Value: 0}}}}},
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: bson.D{
				{Key: "pid", Value: "$pid"},
				{Key: "role_id", Value: "$role_id"},
			}},
			{Key: "total_score", Value: bson.D{{Key: "$sum", Value: "$score"}}},
			{Key: "rb3_score", Value: bson.D{{Key: "$sum", Value: bson.D{
				{Key: "$cond", Value: bson.A{
					bson.D{{Key: "$and", Value: bson.A{
						bson.D{{Key: "$gte", Value: bson.A{"$song_id", RB3OnDiscSongIDMin}}},
						bson.D{{Key: "$lte", Value: bson.A{"$song_id", RB3OnDiscSongIDMax}}},
					}}},
					"$score",
					0,
				}},
			}}}},
		}}},
	}

	cursor, err := db.Collection("scores").Aggregate(ctx, pipeline)
	if err != nil {
		return 0, fmt.Errorf("aggregate scores for role_ranks: %w", err)
	}
	defer cursor.Close(ctx)

	const batchSize = 1000
	batch := make([]interface{}, 0, batchSize)
	total := 0

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if _, err := coll.InsertMany(ctx, batch); err != nil {
			return err
		}
		total += len(batch)
		batch = batch[:0]
		return nil
	}

	for cursor.Next(ctx) {
		var row struct {
			ID struct {
				PID    int `bson:"pid"`
				RoleID int `bson:"role_id"`
			} `bson:"_id"`
			TotalScore int `bson:"total_score"`
			RB3Score   int `bson:"rb3_score"`
		}
		if err := cursor.Decode(&row); err != nil {
			return total, fmt.Errorf("decode role_ranks aggregate row: %w", err)
		}
		batch = append(batch, models.RoleRank{
			PID:        row.ID.PID,
			RoleID:     row.ID.RoleID,
			TotalScore: row.TotalScore,
			RB3Score:   row.RB3Score,
		})
		if len(batch) >= batchSize {
			if err := flush(); err != nil {
				return total, fmt.Errorf("insert role_ranks batch: %w", err)
			}
		}
	}
	if err := cursor.Err(); err != nil {
		return total, fmt.Errorf("role_ranks aggregate cursor: %w", err)
	}
	if err := flush(); err != nil {
		return total, fmt.Errorf("insert role_ranks batch: %w", err)
	}

	return total, nil
}

func roleRankScoreField(rb3Only bool) string {
	if rb3Only {
		return "rb3_score"
	}
	return "total_score"
}

func roleRankPageFilter(roleID int, rb3Only bool) bson.M {
	filter := bson.M{"role_id": roleID}
	if rb3Only {
		filter["rb3_score"] = bson.M{"$gt": 0}
	}
	return filter
}

func roleRankPlayerScore(row models.RoleRank, rb3Only bool) (score int, onBoard bool) {
	if rb3Only {
		if row.RB3Score <= 0 {
			return 0, false
		}
		return row.RB3Score, true
	}
	return row.TotalScore, true
}

// GetRoleRankPage returns a page of per-role rankings from the materialized role_ranks collection.
func GetRoleRankPage(ctx context.Context, db *mongo.Database, opts RoleRankPageOptions) ([]RoleTotalEntry, error) {
	if opts.PageSize < 1 {
		opts.PageSize = 20
	}
	if opts.Page < 1 {
		opts.Page = 1
	}

	coll := db.Collection(RoleRanksCollectionName)
	filter := roleRankPageFilter(opts.RoleID, opts.RB3Only)
	scoreField := roleRankScoreField(opts.RB3Only)

	var skip int64
	if opts.PID != nil {
		row, found, err := GetRoleRank(ctx, db, *opts.PID, opts.RoleID)
		if err != nil {
			return nil, err
		}
		playerScore, onBoard := roleRankPlayerScore(row, opts.RB3Only)
		if !found || !onBoard {
			total, err := coll.CountDocuments(ctx, filter)
			if err != nil {
				return nil, err
			}
			if total == 0 {
				return []RoleTotalEntry{}, nil
			}
			skip = ((total - 1) / int64(opts.PageSize)) * int64(opts.PageSize)
		} else {
			higherFilter := bson.M{}
			for k, v := range filter {
				higherFilter[k] = v
			}
			higherFilter[scoreField] = bson.M{"$gt": playerScore}
			higher, err := coll.CountDocuments(ctx, higherFilter)
			if err != nil {
				return nil, err
			}
			rank0 := higher
			skip = rank0 - (rank0 % int64(opts.PageSize))
		}
	} else {
		skip = int64((opts.Page - 1) * opts.PageSize)
	}

	limit := int64(opts.PageSize)
	cursor, err := coll.Find(ctx, filter, &options.FindOptions{
		Skip:  &skip,
		Limit: &limit,
		Sort:  bson.D{{Key: scoreField, Value: -1}},
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var rows []models.RoleRank
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, err
	}

	entries := make([]RoleTotalEntry, 0, len(rows))
	rank := int(skip) + 1
	for _, row := range rows {
		score, _ := roleRankPlayerScore(row, opts.RB3Only)
		entries = append(entries, RoleTotalEntry{
			PID:        row.PID,
			TotalScore: score,
			Rank:       rank,
		})
		rank++
	}
	return entries, nil
}
