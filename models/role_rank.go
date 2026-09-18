package models

// RoleRank is one player's materialized total-score totals for a single role.
type RoleRank struct {
	PID        int `bson:"pid"`
	RoleID     int `bson:"role_id"`
	TotalScore int `bson:"total_score"` // non-battle/non-setlist sum
	RB3Score   int `bson:"rb3_score"`   // subset: song_id in [1001, 1106]
}
