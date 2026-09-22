package restapi

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"

	database "rb3server/database"

	"go.mongodb.org/mongo-driver/mongo"
)

// RoleRankEntry is one row on a role-rank leaderboard (legacy or materialized).
type RoleRankEntry struct {
	PID        int    `json:"pid"`
	Name       string `json:"name"`
	TotalScore int    `json:"total_score"`
	Rank       int    `json:"rank"`
}

// RoleRankLegacyEntry is kept for existing tests and callers.
type RoleRankLegacyEntry = RoleRankEntry

func parseOptionalBoolQuery(raw string) (value bool, ok bool) {
	if raw == "" {
		return false, true
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

func parseRoleRankQuery(r *http.Request) (database.RoleRankPageOptions, error) {
	roleIDStr := r.URL.Query().Get("role_id")
	if roleIDStr == "" {
		return database.RoleRankPageOptions{}, errRoleRankBadRequest("role_id is required")
	}
	roleID, err := strconv.Atoi(roleIDStr)
	if err != nil {
		return database.RoleRankPageOptions{}, errRoleRankBadRequest("Invalid role_id")
	}

	page := 1
	pageStr := r.URL.Query().Get("page")
	if pageStr != "" {
		page, err = strconv.Atoi(pageStr)
		if err != nil || page < 1 {
			return database.RoleRankPageOptions{}, errRoleRankBadRequest("Invalid page number")
		}
	}

	pageSize := 20
	pageSizeStr := r.URL.Query().Get("page_size")
	if pageSizeStr != "" {
		pageSize, err = strconv.Atoi(pageSizeStr)
		if err != nil || pageSize < 1 || pageSize > 100 {
			return database.RoleRankPageOptions{}, errRoleRankBadRequest("Invalid page_size")
		}
	}

	var pidPtr *int
	pidStr := r.URL.Query().Get("pid")
	if pidStr != "" {
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			return database.RoleRankPageOptions{}, errRoleRankBadRequest("Invalid pid")
		}
		pidPtr = &pid
	}

	rb3Only, ok := parseOptionalBoolQuery(r.URL.Query().Get("rb3_only"))
	if !ok {
		return database.RoleRankPageOptions{}, errRoleRankBadRequest("Invalid rb3_only")
	}

	return database.RoleRankPageOptions{
		RoleID:   roleID,
		Page:     page,
		PageSize: pageSize,
		PID:      pidPtr,
		RB3Only:  rb3Only,
	}, nil
}

type roleRankBadRequest struct {
	msg string
}

func (e roleRankBadRequest) Error() string { return e.msg }

func errRoleRankBadRequest(msg string) error {
	return roleRankBadRequest{msg: msg}
}

func resolveRoleRankNames(ctx context.Context, roleID int, entries []database.RoleTotalEntry) []RoleRankEntry {
	pids := make([]int, 0, len(entries))
	for _, e := range entries {
		pids = append(pids, e.PID)
	}

	leaderboard := make([]RoleRankEntry, 0, len(entries))

	if roleID == 10 {
		bandNameMap, err := database.GetBandNamesByOwnerPIDs(ctx, database.GocentralDatabase, pids)
		if err != nil {
			log.Println("Error fetching band names:", err)
			bandNameMap = make(map[int]string)
		}
		bandOwnerNameMap, err := database.GetUsernamesByPIDs(ctx, database.GocentralDatabase, pids)
		if err != nil {
			log.Println("Error fetching band owner usernames:", err)
			bandOwnerNameMap = make(map[int]string)
		}

		for _, e := range entries {
			name := bandNameMap[e.PID]
			if name == "" {
				name = "Unnamed Band"
				if ownerName := bandOwnerNameMap[e.PID]; ownerName != "" {
					name = ownerName + "'s Band"
				}
			}
			leaderboard = append(leaderboard, RoleRankEntry{
				PID:        e.PID,
				Name:       name,
				TotalScore: e.TotalScore,
				Rank:       e.Rank,
			})
		}
		return leaderboard
	}

	userNameMap, err := database.GetConsolePrefixedUsernamesByPIDs(ctx, database.GocentralDatabase, pids)
	if err != nil {
		log.Println("Error fetching usernames:", err)
		userNameMap = make(map[int]string)
	}

	for _, e := range entries {
		name := "Unnamed Player"
		if n, ok := userNameMap[e.PID]; ok && n != "" {
			name = n
		}
		leaderboard = append(leaderboard, RoleRankEntry{
			PID:        e.PID,
			Name:       name,
			TotalScore: e.TotalScore,
			Rank:       e.Rank,
		})
	}
	return leaderboard
}

func serveRoleRankLeaderboard(w http.ResponseWriter, r *http.Request, fetch func(context.Context, database.RoleRankPageOptions) ([]database.RoleTotalEntry, error), errLabel string) {
	w.Header().Set("Content-Type", "application/json")
	AddStandardHeaders(w)

	opts, err := parseRoleRankQuery(r)
	if err != nil {
		sendError(w, http.StatusBadRequest, err.Error())
		return
	}

	entries, err := fetch(r.Context(), opts)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			sendJSON(w, http.StatusOK, map[string][]RoleRankEntry{"leaderboard": {}})
			return
		}
		log.Printf("ERROR: %s query failed: %v", errLabel, err)
		sendError(w, http.StatusInternalServerError, "Failed to query role rank leaderboard")
		return
	}

	leaderboard := resolveRoleRankNames(r.Context(), opts.RoleID, entries)
	if leaderboard == nil {
		leaderboard = []RoleRankEntry{}
	}
	sendJSON(w, http.StatusOK, map[string][]RoleRankEntry{"leaderboard": leaderboard})
}

// RoleRankHandler serves GET /leaderboards/role-rank from the materialized role_ranks collection.
func RoleRankHandler(w http.ResponseWriter, r *http.Request) {
	serveRoleRankLeaderboard(w, r, func(ctx context.Context, opts database.RoleRankPageOptions) ([]database.RoleTotalEntry, error) {
		return database.GetRoleRankPage(ctx, database.GocentralDatabase, opts)
	}, "role-rank")
}

// RoleRankLegacyHandler serves GET /leaderboards/role-rank/legacy via live score aggregation.
func RoleRankLegacyHandler(w http.ResponseWriter, r *http.Request) {
	serveRoleRankLeaderboard(w, r, func(ctx context.Context, opts database.RoleRankPageOptions) ([]database.RoleTotalEntry, error) {
		return database.GetRoleRankLegacyPage(ctx, database.GocentralDatabase, opts)
	}, "role-rank legacy")
}
