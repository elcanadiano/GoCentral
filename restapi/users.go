package restapi

import (
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	database "rb3server/database"
)

const (
	userSearchMinQueryLen = 2
	userSearchMaxQueryLen = 64
	userSearchLimit       = 5
)

// UserSearchHandler serves GET /users/search?q=<prefix> (case-insensitive autocomplete).
func UserSearchHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	AddStandardHeaders(w)

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		sendError(w, http.StatusBadRequest, "q is required")
		return
	}
	qLen := utf8.RuneCountInString(q)
	if qLen < userSearchMinQueryLen {
		sendError(w, http.StatusBadRequest, "q must be at least 2 characters")
		return
	}
	if qLen > userSearchMaxQueryLen {
		sendError(w, http.StatusBadRequest, "q must be at most 64 characters")
		return
	}

	users, err := database.FindUsersByUsernamePrefix(r.Context(), database.GocentralDatabase, q, userSearchLimit)
	if err != nil {
		log.Printf("ERROR: user search failed: %v", err)
		sendError(w, http.StatusInternalServerError, "Failed to search users")
		return
	}

	sendJSON(w, http.StatusOK, map[string][]database.UserSearchResult{"users": users})
}
