package restapi

import (
	"log"
	"net/http"
	"strconv"

	database "rb3server/database"
)

// PlayerRoleRanksHandler serves GET /role-ranks?pid=<int>.
func PlayerRoleRanksHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	AddStandardHeaders(w)

	pidStr := r.URL.Query().Get("pid")
	if pidStr == "" {
		sendError(w, http.StatusBadRequest, "pid is required")
		return
	}
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		sendError(w, http.StatusBadRequest, "Invalid pid")
		return
	}

	ctx := r.Context()
	user, ok, err := database.GetUserSearchResultByPID(ctx, database.GocentralDatabase, pid)
	if err != nil {
		log.Printf("ERROR: role-ranks user lookup failed: %v", err)
		sendError(w, http.StatusInternalServerError, "Failed to look up user")
		return
	}
	if !ok {
		sendError(w, http.StatusNotFound, "User not found")
		return
	}

	rankings, err := database.GetPlayerRoleRankSummaries(ctx, database.GocentralDatabase, pid)
	if err != nil {
		log.Printf("ERROR: role-ranks summary failed: %v", err)
		sendError(w, http.StatusInternalServerError, "Failed to query role ranks")
		return
	}
	if rankings == nil {
		rankings = []database.PlayerRoleRankSummary{}
	}

	sendJSON(w, http.StatusOK, map[string]interface{}{
		"user":     user,
		"rankings": rankings,
	})
}
