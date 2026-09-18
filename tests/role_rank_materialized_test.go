package tests

import (
	"context"
	"testing"

	"rb3server/database"
	"rb3server/models"

	"go.mongodb.org/mongo-driver/bson"
)

func TestApplyRoleRankScoreDelta(t *testing.T) {
	ctx := context.Background()
	db := database.GocentralDatabase
	coll := db.Collection(database.RoleRanksCollectionName)

	pid := 881001
	roleID := 3

	coll.DeleteMany(ctx, bson.M{"pid": pid, "role_id": roleID})
	defer coll.DeleteMany(ctx, bson.M{"pid": pid, "role_id": roleID})

	if err := database.EnsureRoleRankIndexes(ctx, db); err != nil {
		t.Fatalf("EnsureRoleRankIndexes: %v", err)
	}

	t.Run("non-rb3 insert then improve", func(t *testing.T) {
		coll.DeleteMany(ctx, bson.M{"pid": pid, "role_id": roleID})

		if err := database.ApplyRoleRankScoreDelta(ctx, db, pid, roleID, 1000, false); err != nil {
			t.Fatalf("delta insert: %v", err)
		}
		row, ok, err := database.GetRoleRank(ctx, db, pid, roleID)
		if err != nil || !ok {
			t.Fatalf("GetRoleRank after insert: ok=%v err=%v", ok, err)
		}
		if row.TotalScore != 1000 || row.RB3Score != 0 {
			t.Fatalf("after insert: total=%d rb3=%d", row.TotalScore, row.RB3Score)
		}

		if err := database.ApplyRoleRankScoreDelta(ctx, db, pid, roleID, 500, false); err != nil {
			t.Fatalf("delta improve: %v", err)
		}
		row, ok, err = database.GetRoleRank(ctx, db, pid, roleID)
		if err != nil || !ok {
			t.Fatalf("GetRoleRank after improve: ok=%v err=%v", ok, err)
		}
		if row.TotalScore != 1500 || row.RB3Score != 0 {
			t.Fatalf("after improve: total=%d rb3=%d", row.TotalScore, row.RB3Score)
		}
	})

	t.Run("rb3 increments both fields", func(t *testing.T) {
		coll.DeleteMany(ctx, bson.M{"pid": pid, "role_id": roleID})

		if err := database.ApplyRoleRankScoreDelta(ctx, db, pid, roleID, 200, true); err != nil {
			t.Fatalf("rb3 delta: %v", err)
		}
		row, ok, err := database.GetRoleRank(ctx, db, pid, roleID)
		if err != nil || !ok {
			t.Fatalf("GetRoleRank: ok=%v err=%v", ok, err)
		}
		if row.TotalScore != 200 || row.RB3Score != 200 {
			t.Fatalf("rb3: total=%d rb3=%d", row.TotalScore, row.RB3Score)
		}
	})

	t.Run("zero delta is no-op", func(t *testing.T) {
		coll.DeleteMany(ctx, bson.M{"pid": pid, "role_id": roleID})
		if err := database.ApplyRoleRankScoreDelta(ctx, db, pid, roleID, 0, true); err != nil {
			t.Fatalf("zero delta: %v", err)
		}
		_, ok, err := database.GetRoleRank(ctx, db, pid, roleID)
		if err != nil {
			t.Fatalf("GetRoleRank: %v", err)
		}
		if ok {
			t.Fatal("expected no document after zero delta")
		}
	})
}

func TestIsRB3OnDiscSong(t *testing.T) {
	if !database.IsRB3OnDiscSong(1001) || !database.IsRB3OnDiscSong(1106) || !database.IsRB3OnDiscSong(1050) {
		t.Fatal("expected on-disc IDs to be RB3")
	}
	if database.IsRB3OnDiscSong(1000) || database.IsRB3OnDiscSong(1107) || database.IsRB3OnDiscSong(2000) {
		t.Fatal("expected out-of-range IDs not to be RB3")
	}
}

func TestRebuildRoleRanks_MatchesLiveAndExcludesBattleSetlist(t *testing.T) {
	ctx := context.Background()
	db := database.GocentralDatabase
	scores := db.Collection("scores")
	roleRanks := db.Collection(database.RoleRanksCollectionName)

	roleID := 8
	basePID := 882100

	pids := []int{basePID, basePID + 1, basePID + 2}
	scores.DeleteMany(ctx, bson.M{"pid": bson.M{"$in": pids}, "role_id": roleID})
	roleRanks.DeleteMany(ctx, bson.M{"pid": bson.M{"$in": pids}, "role_id": roleID})
	defer func() {
		scores.DeleteMany(ctx, bson.M{"pid": bson.M{"$in": pids}, "role_id": roleID})
		roleRanks.DeleteMany(ctx, bson.M{"pid": bson.M{"$in": pids}, "role_id": roleID})
	}()

	// alice: 300 RB3 + 100 DLC = 400 total, 300 rb3
	// bob: 200 RB3 only
	// carol: only battle/setlist noise (should not appear)
	docs := []interface{}{
		models.Score{OwnerPID: basePID, SongID: 1050, RoleID: roleID, Score: 300, Stars: 5, DiffID: 2, NotesPercent: 90},
		models.Score{OwnerPID: basePID, SongID: 2000, RoleID: roleID, Score: 100, Stars: 5, DiffID: 2, NotesPercent: 90},
		models.Score{OwnerPID: basePID + 1, SongID: 1050, RoleID: roleID, Score: 200, Stars: 5, DiffID: 2, NotesPercent: 90},
		bson.M{"pid": basePID + 2, "song_id": 1050, "role_id": roleID, "score": 99999, "battle_id": 42, "stars": 5, "diff_id": 2, "notespct": 90},
		bson.M{"pid": basePID + 2, "song_id": 1050, "role_id": roleID, "score": 88888, "setlist_id": 99, "stars": 5, "diff_id": 2, "notespct": 90},
	}
	if _, err := scores.InsertMany(ctx, docs); err != nil {
		t.Fatalf("InsertMany scores: %v", err)
	}

	// Rebuild wipes the whole collection — only safe in gocentral_test.
	n, err := database.RebuildRoleRanks(ctx, db)
	if err != nil {
		t.Fatalf("RebuildRoleRanks: %v", err)
	}
	if n < 2 {
		t.Fatalf("expected at least 2 role_ranks docs from fixtures (+ any other test residue), got %d", n)
	}

	alice, ok, err := database.GetRoleRank(ctx, db, basePID, roleID)
	if err != nil || !ok {
		t.Fatalf("alice row: ok=%v err=%v", ok, err)
	}
	if alice.TotalScore != 400 || alice.RB3Score != 300 {
		t.Fatalf("alice: total=%d rb3=%d want 400/300", alice.TotalScore, alice.RB3Score)
	}

	bob, ok, err := database.GetRoleRank(ctx, db, basePID+1, roleID)
	if err != nil || !ok {
		t.Fatalf("bob row: ok=%v err=%v", ok, err)
	}
	if bob.TotalScore != 200 || bob.RB3Score != 200 {
		t.Fatalf("bob: total=%d rb3=%d want 200/200", bob.TotalScore, bob.RB3Score)
	}

	_, ok, err = database.GetRoleRank(ctx, db, basePID+2, roleID)
	if err != nil {
		t.Fatalf("carol lookup: %v", err)
	}
	if ok {
		t.Fatal("carol should not have a role_ranks row (battle/setlist only)")
	}

	// Cross-check against live legacy aggregation totals for alice/bob
	live, err := database.GetRoleRankLegacyPage(ctx, db, database.RoleRankLegacyPageOptions{
		RoleID:   roleID,
		Page:     1,
		PageSize: 100,
	})
	if err != nil {
		t.Fatalf("GetRoleRankLegacyPage: %v", err)
	}
	got := map[int]int{}
	for _, e := range live {
		if e.PID == basePID || e.PID == basePID+1 {
			got[e.PID] = e.TotalScore
		}
	}
	if got[basePID] != 400 || got[basePID+1] != 200 {
		t.Fatalf("live totals mismatch: %#v", got)
	}
}
