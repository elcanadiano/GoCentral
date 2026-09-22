package tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"rb3server/database"
	"rb3server/models"
	"rb3server/restapi"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

func TestPlayerRoleRanksHandler_Validation(t *testing.T) {
	testCases := []struct {
		name  string
		query string
	}{
		{"Missing pid", "/role-ranks"},
		{"Invalid pid", "/role-ranks?pid=abc"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.query, nil)
			rr := httptest.NewRecorder()
			restapi.PlayerRoleRanksHandler(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("Expected 400, got %d (%s)", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestPlayerRoleRanksHandler_NotFound(t *testing.T) {
	req := httptest.NewRequest("GET", "/role-ranks?pid=887999001", nil)
	rr := httptest.NewRecorder()
	restapi.PlayerRoleRanksHandler(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("Expected 404, got %d (%s)", rr.Code, rr.Body.String())
	}
}

func TestPlayerRoleRanksHandler_Summaries(t *testing.T) {
	ctx := context.Background()
	db := database.GocentralDatabase
	users := db.Collection("users")
	roleRanks := db.Collection(database.RoleRanksCollectionName)

	pid := 887100
	otherPID := 887101
	roleID := 1

	users.DeleteMany(ctx, bson.M{"pid": bson.M{"$in": []int{pid, otherPID}}})
	roleRanks.DeleteMany(ctx, bson.M{"pid": bson.M{"$in": []int{pid, otherPID}}, "role_id": roleID})
	defer func() {
		users.DeleteMany(ctx, bson.M{"pid": bson.M{"$in": []int{pid, otherPID}}})
		roleRanks.DeleteMany(ctx, bson.M{"pid": bson.M{"$in": []int{pid, otherPID}}, "role_id": roleID})
	}()

	if err := database.EnsureRoleRankIndexes(ctx, db); err != nil {
		t.Fatalf("EnsureRoleRankIndexes: %v", err)
	}

	users.InsertOne(ctx, bson.M{"pid": pid, "username": "prr_alice", "console_type": 3})
	users.InsertOne(ctx, bson.M{"pid": otherPID, "username": "prr_bob", "console_type": 1})

	t.Run("empty rankings", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/role-ranks?pid=887100", nil)
		rr := httptest.NewRecorder()
		restapi.PlayerRoleRanksHandler(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d (%s)", rr.Code, rr.Body.String())
		}
		var response struct {
			User     database.UserSearchResult         `json:"user"`
			Rankings []database.PlayerRoleRankSummary `json:"rankings"`
		}
		decodeResponse(t, rr, &response)
		if response.User.PID != pid || response.User.Username != "prr_alice" || response.User.ConsoleType != 3 {
			t.Fatalf("unexpected user: %+v", response.User)
		}
		if response.Rankings == nil || len(response.Rankings) != 0 {
			t.Fatalf("expected empty rankings, got %+v", response.Rankings)
		}
	})

	roleRanks.InsertMany(ctx, []interface{}{
		models.RoleRank{PID: pid, RoleID: roleID, TotalScore: 300, RB3Score: 0},
		models.RoleRank{PID: otherPID, RoleID: roleID, TotalScore: 500, RB3Score: 400},
		models.RoleRank{PID: pid, RoleID: 2, TotalScore: 100, RB3Score: 100},
	})
	defer roleRanks.DeleteMany(ctx, bson.M{"pid": pid, "role_id": 2})

	// Ensure only our two players matter for rank math on role 1: clear other role_id=1 residue? 
	// Safer: delete all role_id=1 then reinsert just our two.
	roleRanks.DeleteMany(ctx, bson.M{"role_id": roleID})
	roleRanks.InsertMany(ctx, []interface{}{
		models.RoleRank{PID: pid, RoleID: roleID, TotalScore: 300, RB3Score: 0},
		models.RoleRank{PID: otherPID, RoleID: roleID, TotalScore: 500, RB3Score: 400},
	})

	t.Run("rankings with rb3_rank zero", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/role-ranks?pid=887100", nil)
		rr := httptest.NewRecorder()
		restapi.PlayerRoleRanksHandler(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d (%s)", rr.Code, rr.Body.String())
		}
		var response struct {
			User     database.UserSearchResult         `json:"user"`
			Rankings []database.PlayerRoleRankSummary `json:"rankings"`
		}
		decodeResponse(t, rr, &response)
		if len(response.Rankings) != 2 {
			t.Fatalf("expected 2 role rows, got %+v", response.Rankings)
		}
		byRole := map[int]database.PlayerRoleRankSummary{}
		for _, r := range response.Rankings {
			byRole[r.RoleID] = r
		}
		r1 := byRole[1]
		if r1.TotalScore != 300 || r1.TotalRank != 2 {
			t.Fatalf("role 1 total: %+v want score 300 rank 2", r1)
		}
		if r1.RB3Score != 0 || r1.RB3Rank != 0 {
			t.Fatalf("role 1 rb3: %+v want score 0 rank 0", r1)
		}
		r2 := byRole[2]
		if r2.TotalScore != 100 || r2.RB3Score != 100 || r2.RB3Rank != 1 {
			t.Fatalf("role 2: %+v", r2)
		}
	})
}
