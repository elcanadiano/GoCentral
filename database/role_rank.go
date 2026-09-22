package database

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// RoleTotalEntry is one row on a per-role total-score leaderboard.
type RoleTotalEntry struct {
	PID        int
	TotalScore int
	Rank       int
}

// RoleRankPageOptions controls role-rank leaderboard paging (legacy and materialized).
type RoleRankPageOptions struct {
	RoleID   int
	Page     int  // 1-based; ignored when PID is set
	PageSize int
	PID      *int // when set, return the page window containing this player
	RB3Only  bool
}

// RoleRankLegacyPageOptions is kept as an alias for callers of the live aggregation path.
type RoleRankLegacyPageOptions = RoleRankPageOptions

func roleTotalsMatch(roleID int, rb3Only bool) bson.D {
	match := bson.D{
		{Key: "battle_id", Value: bson.D{{Key: "$not", Value: bson.D{{Key: "$gt", Value: 0}}}}},
		{Key: "setlist_id", Value: bson.D{{Key: "$not", Value: bson.D{{Key: "$gt", Value: 0}}}}},
		{Key: "role_id", Value: roleID},
	}
	if rb3Only {
		match = append(match, bson.E{Key: "song_id", Value: bson.D{{Key: "$gte", Value: 1001}, {Key: "$lte", Value: 1106}}})
	}
	return match
}

func roleTotalsGroupSortPipeline(match bson.D) mongo.Pipeline {
	return mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$pid"},
			{Key: "totalScore", Value: bson.D{{Key: "$sum", Value: "$score"}}},
		}}},
		{{Key: "$sort", Value: bson.D{{Key: "totalScore", Value: -1}}}},
	}
}

func countRoleTotalPlayers(ctx context.Context, scores *mongo.Collection, match bson.D) (int64, error) {
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$group", Value: bson.D{{Key: "_id", Value: "$pid"}}}},
		{{Key: "$count", Value: "total"}},
	}
	cursor, err := scores.Aggregate(ctx, pipeline)
	if err != nil {
		return 0, err
	}
	defer cursor.Close(ctx)

	var results []struct {
		Total int64 `bson:"total"`
	}
	if err := cursor.All(ctx, &results); err != nil {
		return 0, err
	}
	if len(results) == 0 {
		return 0, nil
	}
	return results[0].Total, nil
}

func getPlayerRoleTotal(ctx context.Context, scores *mongo.Collection, match bson.D, pid int) (total int, found bool, err error) {
	playerMatch := append(bson.D{}, match...)
	playerMatch = append(playerMatch, bson.E{Key: "pid", Value: pid})

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: playerMatch}},
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$pid"},
			{Key: "totalScore", Value: bson.D{{Key: "$sum", Value: "$score"}}},
		}}},
	}
	cursor, err := scores.Aggregate(ctx, pipeline)
	if err != nil {
		return 0, false, err
	}
	defer cursor.Close(ctx)

	var results []struct {
		TotalScore int `bson:"totalScore"`
	}
	if err := cursor.All(ctx, &results); err != nil {
		return 0, false, err
	}
	if len(results) == 0 {
		return 0, false, nil
	}
	return results[0].TotalScore, true, nil
}

func countPlayersWithHigherRoleTotal(ctx context.Context, scores *mongo.Collection, match bson.D, totalScore int) (int64, error) {
	pipeline := append(roleTotalsGroupSortPipeline(match),
		bson.D{{Key: "$match", Value: bson.D{{Key: "totalScore", Value: bson.D{{Key: "$gt", Value: totalScore}}}}}},
		bson.D{{Key: "$count", Value: "total"}},
	)
	cursor, err := scores.Aggregate(ctx, pipeline)
	if err != nil {
		return 0, err
	}
	defer cursor.Close(ctx)

	var results []struct {
		Total int64 `bson:"total"`
	}
	if err := cursor.All(ctx, &results); err != nil {
		return 0, err
	}
	if len(results) == 0 {
		return 0, nil
	}
	return results[0].Total, nil
}

// GetRoleRankLegacyPage returns a page of per-role total-score rankings (live aggregation).
func GetRoleRankLegacyPage(ctx context.Context, db *mongo.Database, opts RoleRankLegacyPageOptions) ([]RoleTotalEntry, error) {
	if opts.PageSize < 1 {
		opts.PageSize = 20
	}
	if opts.Page < 1 {
		opts.Page = 1
	}

	scores := db.Collection("scores")
	match := roleTotalsMatch(opts.RoleID, opts.RB3Only)

	var skip int64
	if opts.PID != nil {
		playerTotal, found, err := getPlayerRoleTotal(ctx, scores, match, *opts.PID)
		if err != nil {
			return nil, err
		}
		if !found {
			total, err := countRoleTotalPlayers(ctx, scores, match)
			if err != nil {
				return nil, err
			}
			if total == 0 {
				return []RoleTotalEntry{}, nil
			}
			// last page (0-based skip)
			skip = ((total - 1) / int64(opts.PageSize)) * int64(opts.PageSize)
		} else {
			higher, err := countPlayersWithHigherRoleTotal(ctx, scores, match, playerTotal)
			if err != nil {
				return nil, err
			}
			rank0 := higher // 0-based index
			skip = rank0 - (rank0 % int64(opts.PageSize))
		}
	} else {
		skip = int64((opts.Page - 1) * opts.PageSize)
	}

	pipeline := append(roleTotalsGroupSortPipeline(match),
		bson.D{{Key: "$skip", Value: skip}},
		bson.D{{Key: "$limit", Value: int64(opts.PageSize)}},
	)

	cursor, err := scores.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var raw []struct {
		PID        int `bson:"_id"`
		TotalScore int `bson:"totalScore"`
	}
	if err := cursor.All(ctx, &raw); err != nil {
		return nil, err
	}

	entries := make([]RoleTotalEntry, 0, len(raw))
	rank := int(skip) + 1
	for _, row := range raw {
		entries = append(entries, RoleTotalEntry{
			PID:        row.PID,
			TotalScore: row.TotalScore,
			Rank:       rank,
		})
		rank++
	}
	return entries, nil
}
