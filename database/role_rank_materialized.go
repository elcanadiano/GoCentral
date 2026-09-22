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
	return roleRankBoardFilter(roleID, rb3Only, nil)
}

func roleRankBoardFilter(roleID int, rb3Only bool, pids []int) bson.M {
	filter := bson.M{"role_id": roleID}
	if rb3Only {
		filter["rb3_score"] = bson.M{"$gt": 0}
	}
	if len(pids) > 0 {
		filter["pid"] = bson.M{"$in": pids}
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

func copyFilter(src bson.M) bson.M {
	dst := bson.M{}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// CountRoleRankPlayers returns how many players are on the materialized board.
func CountRoleRankPlayers(ctx context.Context, db *mongo.Database, roleID int, rb3Only bool, pids []int) (int64, error) {
	return db.Collection(RoleRanksCollectionName).CountDocuments(ctx, roleRankBoardFilter(roleID, rb3Only, pids))
}

// GetRoleRankEntriesBySkip returns a sorted page from role_ranks starting at 0-based skip.
func GetRoleRankEntriesBySkip(ctx context.Context, db *mongo.Database, roleID int, rb3Only bool, skip, limit int64, pids []int) ([]RoleTotalEntry, error) {
	if limit < 1 {
		return []RoleTotalEntry{}, nil
	}
	if skip < 0 {
		skip = 0
	}

	filter := roleRankBoardFilter(roleID, rb3Only, pids)
	scoreField := roleRankScoreField(rb3Only)
	coll := db.Collection(RoleRanksCollectionName)

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
		score, _ := roleRankPlayerScore(row, rb3Only)
		entries = append(entries, RoleTotalEntry{
			PID:        row.PID,
			TotalScore: score,
			Rank:       rank,
		})
		rank++
	}
	return entries, nil
}

// GetRoleRankPlayerWindow returns a pageSize window aligned to rank0 % pageSize containing pid.
// If the player is missing from the board, returns the top page (game player/get parity).
func GetRoleRankPlayerWindow(ctx context.Context, db *mongo.Database, roleID int, rb3Only bool, pid, pageSize int, pids []int) ([]RoleTotalEntry, error) {
	if pageSize < 1 {
		pageSize = 19
	}

	filter := roleRankBoardFilter(roleID, rb3Only, pids)
	scoreField := roleRankScoreField(rb3Only)
	coll := db.Collection(RoleRanksCollectionName)

	var rank0 int64
	row, found, err := GetRoleRank(ctx, db, pid, roleID)
	if err != nil {
		return nil, err
	}
	playerScore, onBoard := roleRankPlayerScore(row, rb3Only)
	if found && onBoard {
		// Player must also pass optional pid filter (friends/console).
		if len(pids) > 0 {
			inFilter := false
			for _, p := range pids {
				if p == pid {
					inFilter = true
					break
				}
			}
			if !inFilter {
				onBoard = false
			}
		}
	}
	if found && onBoard {
		higherFilter := copyFilter(filter)
		higherFilter[scoreField] = bson.M{"$gt": playerScore}
		higher, err := coll.CountDocuments(ctx, higherFilter)
		if err != nil {
			return nil, err
		}
		rank0 = higher
	} else {
		// Missing player → top page (matches live player.go aggregated path).
		rank0 = 0
	}

	skip := rank0 - (rank0 % int64(pageSize))
	return GetRoleRankEntriesBySkip(ctx, db, roleID, rb3Only, skip, int64(pageSize), pids)
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
			higherFilter := copyFilter(filter)
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

	return GetRoleRankEntriesBySkip(ctx, db, opts.RoleID, opts.RB3Only, skip, int64(opts.PageSize), nil)
}

// PlayerRoleRankSummary is one role's totals and ranks for a single player.
type PlayerRoleRankSummary struct {
	RoleID     int `json:"role_id"`
	TotalScore int `json:"total_score"`
	TotalRank  int `json:"total_rank"`
	RB3Score   int `json:"rb3_score"`
	RB3Rank    int `json:"rb3_rank"`
}

// ListRoleRanksForPID returns all role_ranks docs for pid, ordered by role_id ascending.
func ListRoleRanksForPID(ctx context.Context, db *mongo.Database, pid int) ([]models.RoleRank, error) {
	cursor, err := db.Collection(RoleRanksCollectionName).Find(
		ctx,
		bson.M{"pid": pid},
		options.Find().SetSort(bson.D{{Key: "role_id", Value: 1}}),
	)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var rows []models.RoleRank
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []models.RoleRank{}
	}
	return rows, nil
}

func countHigherRoleRankScore(ctx context.Context, db *mongo.Database, roleID int, rb3Only bool, score int) (int64, error) {
	filter := roleRankBoardFilter(roleID, rb3Only, nil)
	filter[roleRankScoreField(rb3Only)] = bson.M{"$gt": score}
	return db.Collection(RoleRanksCollectionName).CountDocuments(ctx, filter)
}

// GetPlayerRoleRankSummaries returns per-role totals and ranks for pid from role_ranks.
// Roles without a document are omitted. rb3_rank is 0 when rb3_score is 0.
func GetPlayerRoleRankSummaries(ctx context.Context, db *mongo.Database, pid int) ([]PlayerRoleRankSummary, error) {
	rows, err := ListRoleRanksForPID(ctx, db, pid)
	if err != nil {
		return nil, err
	}

	summaries := make([]PlayerRoleRankSummary, 0, len(rows))
	for _, row := range rows {
		higherTotal, err := countHigherRoleRankScore(ctx, db, row.RoleID, false, row.TotalScore)
		if err != nil {
			return nil, err
		}
		totalRank := int(higherTotal) + 1

		rb3Rank := 0
		if row.RB3Score > 0 {
			higherRB3, err := countHigherRoleRankScore(ctx, db, row.RoleID, true, row.RB3Score)
			if err != nil {
				return nil, err
			}
			rb3Rank = int(higherRB3) + 1
		}

		summaries = append(summaries, PlayerRoleRankSummary{
			RoleID:     row.RoleID,
			TotalScore: row.TotalScore,
			TotalRank:  totalRank,
			RB3Score:   row.RB3Score,
			RB3Rank:    rb3Rank,
		})
	}
	return summaries, nil
}
