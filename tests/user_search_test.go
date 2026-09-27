package tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"rb3server/database"
	"rb3server/restapi"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

func TestUserSearchHandler_Validation(t *testing.T) {
	testCases := []struct {
		name  string
		query string
	}{
		{"Missing q", "/users/search"},
		{"Empty q", "/users/search?q="},
		{"Too short", "/users/search?q=a"},
		{"Too long", "/users/search?q=" + string(make([]byte, 65))},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			q := tc.query
			if tc.name == "Too long" {
				long := make([]rune, 65)
				for i := range long {
					long[i] = 'x'
				}
				q = "/users/search?q=" + string(long)
			}
			req := httptest.NewRequest("GET", q, nil)
			rr := httptest.NewRecorder()
			restapi.UserSearchHandler(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("Expected status 400, got %d (body: %s)", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestUserSearchHandler_SubstringAndCap(t *testing.T) {
	ctx := context.Background()
	users := database.GocentralDatabase.Collection("users")

	basePID := 886100
	names := []string{
		"elcanadiano",
		"elcan_other",
		"ELCAN_UPPER",
		"xxnadiaxx",
		"other_user_a",
		"other_user_b",
		"other_user_c",
		"other_user_d",
		"other_user_e",
		"other_user_f",
	}

	pids := make([]int, len(names))
	for i, name := range names {
		pids[i] = basePID + i
		users.DeleteOne(ctx, bson.M{"pid": pids[i]})
		_, err := users.InsertOne(ctx, bson.M{
			"pid": pids[i], "username": name, "console_type": i % 4,
		})
		if err != nil {
			t.Fatalf("InsertOne: %v", err)
		}
	}
	defer users.DeleteMany(ctx, bson.M{"pid": bson.M{"$in": pids}})

	t.Run("prefix match elcan", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/users/search?q=elcan", nil)
		rr := httptest.NewRecorder()
		restapi.UserSearchHandler(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d (%s)", rr.Code, rr.Body.String())
		}
		var response map[string][]database.UserSearchResult
		decodeResponse(t, rr, &response)
		got := response["users"]
		if len(got) < 3 {
			t.Fatalf("expected at least 3 elcan* matches, got %+v", got)
		}
		found := map[int]bool{}
		for _, u := range got {
			found[u.PID] = true
			if u.Username == "" {
				t.Errorf("empty username for pid %d", u.PID)
			}
		}
		if !found[basePID] || !found[basePID+1] || !found[basePID+2] {
			t.Fatalf("expected elcanadiano/elcan_other/ELCAN_UPPER, got %+v", got)
		}
		for _, u := range got {
			if u.PID == basePID+3 {
				t.Fatal("xxnadiaxx should not match prefix elcan")
			}
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/users/search?q=ELCAN", nil)
		rr := httptest.NewRecorder()
		restapi.UserSearchHandler(rr, req)
		var response map[string][]database.UserSearchResult
		decodeResponse(t, rr, &response)
		found := false
		for _, u := range response["users"] {
			if u.PID == basePID {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected elcanadiano for ELCAN, got %+v", response["users"])
		}
	})

	t.Run("cap at 5", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/users/search?q=other_user", nil)
		rr := httptest.NewRecorder()
		restapi.UserSearchHandler(rr, req)
		var response map[string][]database.UserSearchResult
		decodeResponse(t, rr, &response)
		if len(response["users"]) != 5 {
			t.Fatalf("expected 5 results, got %d (%+v)", len(response["users"]), response["users"])
		}
	})

	t.Run("empty result", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/users/search?q=zzznomatchzzz", nil)
		rr := httptest.NewRecorder()
		restapi.UserSearchHandler(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", rr.Code)
		}
		var response map[string][]database.UserSearchResult
		decodeResponse(t, rr, &response)
		if response["users"] == nil || len(response["users"]) != 0 {
			t.Fatalf("expected empty users, got %+v", response["users"])
		}
	})

	t.Run("response shape", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/users/search?q=elcanadiano", nil)
		rr := httptest.NewRecorder()
		restapi.UserSearchHandler(rr, req)
		var response map[string][]database.UserSearchResult
		decodeResponse(t, rr, &response)
		if len(response["users"]) != 1 {
			t.Fatalf("expected 1, got %+v", response["users"])
		}
		u := response["users"][0]
		if u.PID != basePID || u.Username != "elcanadiano [360]" {
			t.Fatalf("unexpected user: %+v", u)
		}
	})
}
