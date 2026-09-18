package restapi

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	database "rb3server/database"

	"go.mongodb.org/mongo-driver/mongo"
)

// RoleRankLegacyEntry is one row on the legacy role-rank leaderboard.
type RoleRankLegacyEntry struct {
	PID        int    `json:"pid"`
	Name       string `json:"name"`
	TotalScore int    `json:"total_score"`
	Rank       int    `json:"rank"`
}

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

// RoleRankLegacyHandler serves GET /leaderboards/role-rank/legacy.
func RoleRankLegacyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	AddStandardHeaders(w)

	roleIDStr := r.URL.Query().Get("role_id")
	if roleIDStr == "" {
		sendError(w, http.StatusBadRequest, "role_id is required")
		return
	}
	roleID, err := strconv.Atoi(roleIDStr)
	if err != nil {
		sendError(w, http.StatusBadRequest, "Invalid role_id")
		return
	}

	page := 1
	pageStr := r.URL.Query().Get("page")
	if pageStr != "" {
		page, err = strconv.Atoi(pageStr)
		if err != nil || page < 1 {
			sendError(w, http.StatusBadRequest, "Invalid page number")
			return
		}
	}

	pageSize := 20
	pageSizeStr := r.URL.Query().Get("page_size")
	if pageSizeStr != "" {
		pageSize, err = strconv.Atoi(pageSizeStr)
		if err != nil || pageSize < 1 || pageSize > 100 {
			sendError(w, http.StatusBadRequest, "Invalid page_size")
			return
		}
	}

	var pidPtr *int
	pidStr := r.URL.Query().Get("pid")
	if pidStr != "" {
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			sendError(w, http.StatusBadRequest, "Invalid pid")
			return
		}
		pidPtr = &pid
	}

	rb3Only, ok := parseOptionalBoolQuery(r.URL.Query().Get("rb3_only"))
	if !ok {
		sendError(w, http.StatusBadRequest, "Invalid rb3_only")
		return
	}

	opts := database.RoleRankLegacyPageOptions{
		RoleID:   roleID,
		Page:     page,
		PageSize: pageSize,
		PID:      pidPtr,
		RB3Only:  rb3Only,
	}

	entries, err := database.GetRoleRankLegacyPage(r.Context(), database.GocentralDatabase, opts)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			sendJSON(w, http.StatusOK, map[string][]RoleRankLegacyEntry{"leaderboard": {}})
			return
		}
		log.Printf("ERROR: role-rank legacy query failed: %v", err)
		sendError(w, http.StatusInternalServerError, "Failed to query role rank leaderboard")
		return
	}

	pids := make([]int, 0, len(entries))
	for _, e := range entries {
		pids = append(pids, e.PID)
	}

	ctx := r.Context()
	var leaderboard []RoleRankLegacyEntry

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
			leaderboard = append(leaderboard, RoleRankLegacyEntry{
				PID:        e.PID,
				Name:       name,
				TotalScore: e.TotalScore,
				Rank:       e.Rank,
			})
		}
	} else {
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
			leaderboard = append(leaderboard, RoleRankLegacyEntry{
				PID:        e.PID,
				Name:       name,
				TotalScore: e.TotalScore,
				Rank:       e.Rank,
			})
		}
	}

	if leaderboard == nil {
		leaderboard = []RoleRankLegacyEntry{}
	}
	sendJSON(w, http.StatusOK, map[string][]RoleRankLegacyEntry{"leaderboard": leaderboard})
}
