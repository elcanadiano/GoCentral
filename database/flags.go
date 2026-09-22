package database

import (
	"os"
	"strings"
)

// UseMaterializedRoleRanks reports whether jsonproto Total Score / RB3-only
// leaderboards should read from the role_ranks collection.
// Truthy: 1, true, yes, on (same tokens as server envTrue). Default off.
func UseMaterializedRoleRanks() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("USE_MATERIALIZED_ROLE_RANKS"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
