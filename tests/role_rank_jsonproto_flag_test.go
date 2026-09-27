package tests

import (
	"context"
	"testing"

	"rb3server/database"
	"rb3server/models"

	"go.mongodb.org/mongo-driver/bson"
)

func TestUseMaterializedRoleRanks(t *testing.T) {
	t.Setenv("USE_MATERIALIZED_ROLE_RANKS", "")
	if database.UseMaterializedRoleRanks() {
		t.Fatal("expected default off")
	}
	for _, v := range []string{"1", "true", "YES", "on"} {
		t.Setenv("USE_MATERIALIZED_ROLE_RANKS", v)
		if !database.UseMaterializedRoleRanks() {
			t.Fatalf("expected truthy %q to enable", v)
		}
	}
	t.Setenv("USE_MATERIALIZED_ROLE_RANKS", "0")
	if database.UseMaterializedRoleRanks() {
		t.Fatal("expected 0 to disable")
	}
}

func TestMaterializedRoleRankJsonprotoHelpers(t *testing.T) {
	ctx := context.Background()
	db := database.GocentralDatabase
	coll := db.Collection(database.RoleRanksCollectionName)

	roleID := 4
	basePID := 885100

	// Isolate this role so absolute ranking assertions are stable.
	coll.DeleteMany(ctx, bson.M{"role_id": roleID})
	defer coll.DeleteMany(ctx, bson.M{"role_id": roleID})

	if err := database.EnsureRoleRankIndexes(ctx, db); err != nil {
		t.Fatalf("EnsureRoleRankIndexes: %v", err)
	}

	docs := []interface{}{
		models.RoleRank{PID: basePID, RoleID: roleID, TotalScore: 500, RB3Score: 500},
		models.RoleRank{PID: basePID + 1, RoleID: roleID, TotalScore: 400, RB3Score: 100},
		models.RoleRank{PID: basePID + 2, RoleID: roleID, TotalScore: 300, RB3Score: 300},
		models.RoleRank{PID: basePID + 3, RoleID: roleID, TotalScore: 200, RB3Score: 200},
		models.RoleRank{PID: basePID + 4, RoleID: roleID, TotalScore: 100, RB3Score: 0},
		models.RoleRank{PID: basePID + 5, RoleID: roleID, TotalScore: 50, RB3Score: 50},
	}
	if _, err := coll.InsertMany(ctx, docs); err != nil {
		t.Fatalf("InsertMany: %v", err)
	}

	t.Run("count total and rb3", func(t *testing.T) {
		n, err := database.CountRoleRankPlayers(ctx, db, roleID, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		if n != 6 {
			t.Fatalf("expected 6 players, got %d", n)
		}
		nRB3, err := database.CountRoleRankPlayers(ctx, db, roleID, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		if nRB3 != 5 {
			t.Fatalf("expected 5 rb3 players, got %d", nRB3)
		}
	})

	t.Run("absolute skip page like rankrange", func(t *testing.T) {
		page, err := database.GetRoleRankEntriesBySkip(ctx, db, roleID, false, 0, 2, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) != 2 {
			t.Fatalf("want 2, got %d", len(page))
		}
		if page[0].PID != basePID || page[0].TotalScore != 500 || page[0].Rank != 1 {
			t.Fatalf("rank1: %+v", page[0])
		}
		if page[1].PID != basePID+1 || page[1].Rank != 2 {
			t.Fatalf("rank2: %+v", page[1])
		}
	})

	t.Run("player window centered with page size 19", func(t *testing.T) {
		// dave is rank 4 (0-based 3) → window still starts at 0 with pageSize 19
		page, err := database.GetRoleRankPlayerWindow(ctx, db, roleID, false, basePID+3, 19, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) < 4 {
			t.Fatalf("expected at least 4 rows, got %d", len(page))
		}
		if page[0].Rank != 1 {
			t.Fatalf("expected top-aligned window start rank 1, got %d", page[0].Rank)
		}
		found := false
		for _, e := range page {
			if e.PID == basePID+3 {
				found = true
				if e.Rank != 4 {
					t.Fatalf("dave rank want 4 got %d", e.Rank)
				}
			}
		}
		if !found {
			t.Fatal("expected dave on window")
		}
	})

	t.Run("missing player returns top page", func(t *testing.T) {
		page, err := database.GetRoleRankPlayerWindow(ctx, db, roleID, false, 999888777, 2, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) != 2 || page[0].Rank != 1 || page[0].PID != basePID {
			t.Fatalf("expected top page, got %+v", page)
		}
	})

	t.Run("friends pid filter", func(t *testing.T) {
		friendPIDs := []int{basePID + 1, basePID + 3}
		n, err := database.CountRoleRankPlayers(ctx, db, roleID, false, friendPIDs)
		if err != nil {
			t.Fatal(err)
		}
		if n != 2 {
			t.Fatalf("expected 2 friends on board, got %d", n)
		}
		page, err := database.GetRoleRankEntriesBySkip(ctx, db, roleID, false, 0, 10, friendPIDs)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(page))
		}
		if page[0].PID != basePID+1 || page[1].PID != basePID+3 {
			t.Fatalf("unexpected order: %+v", page)
		}
	})

	t.Run("rb3_only page excludes zero rb3", func(t *testing.T) {
		page, err := database.GetRoleRankEntriesBySkip(ctx, db, roleID, true, 0, 20, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page {
			if e.PID == basePID+4 {
				t.Fatal("zero rb3_score pid should be excluded")
			}
		}
		if page[0].PID != basePID || page[0].TotalScore != 500 {
			t.Fatalf("rb3 rank1: %+v", page[0])
		}
	})
}
