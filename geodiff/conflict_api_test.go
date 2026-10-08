package geodiff

import (
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	"github.com/BenDyson-Arch/go-geodiff/changeset"
)

func copyAndExec(t *testing.T, src, dst, sqlStmt string) {
	t.Helper()
	if err := FileCopy(dst, src); err != nil {
		t.Fatalf("copy: %v", err)
	}
	db, err := sql.Open("sqlite", dst)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(sqlStmt); err != nil {
		t.Fatalf("exec %q: %v", sqlStmt, err)
	}
}

func seedSimple(t *testing.T, path string) {
	t.Helper()
	createTestDB(t, path, []struct {
		Name  string
		Value int
	}{{"old", 10}})
}

func TestCreateRebasedChangeset_OverlapReturnsTypedConflict(t *testing.T) {
	base := tmpPath(t, "overlap_base.sqlite")
	theirs := tmpPath(t, "overlap_theirs.sqlite")
	ours := tmpPath(t, "overlap_ours.sqlite")
	baseTheirs := tmpPath(t, "overlap_base_theirs.diff")
	rebased := tmpPath(t, "overlap_rebased.diff")
	seedSimple(t, base)
	copyAndExec(t, base, theirs, "UPDATE simple SET name = 'theirs_name' WHERE fid = 1")
	copyAndExec(t, base, ours, "UPDATE simple SET name = 'ours_name' WHERE fid = 1")
	if err := CreateChangeset(base, theirs, baseTheirs); err != nil {
		t.Fatalf("CreateChangeset: %v", err)
	}

	conflicts, err := CreateRebasedChangeset(base, ours, baseTheirs, rebased, "")
	if err != nil {
		t.Fatalf("CreateRebasedChangeset: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	cf := conflicts[0]
	if cf.TableName != "simple" {
		t.Errorf("table: got %q", cf.TableName)
	}
	if cf.PK != 1 {
		t.Errorf("pk: got %d", cf.PK)
	}
	if len(cf.Items) != 1 {
		t.Fatalf("items: got %d", len(cf.Items))
	}
	item := cf.Items[0]
	if item.Column != 1 {
		t.Errorf("column: got %d, want 1 (name)", item.Column)
	}
	if got, _ := item.Base.AsText(); got != "old" {
		t.Errorf("base: got %q", got)
	}
	if got, _ := item.Theirs.AsText(); got != "theirs_name" {
		t.Errorf("theirs: got %q", got)
	}
	if got, _ := item.Ours.AsText(); got != "ours_name" {
		t.Errorf("ours: got %q", got)
	}

	// Blob stays a geodiff changeset (no magic header). Conflicted update is omitted.
	r, err := changeset.NewReader(rebased)
	if err != nil {
		t.Fatalf("rebased blob is not a changeset: %v", err)
	}
	r.Close()

	// Typed values round-trip through JSON for server storage.
	raw, err := json.Marshal(conflicts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back []ConflictFeature
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back) != 1 || back[0].PK != 1 {
		t.Fatalf("round-trip: %+v", back)
	}
	if got, _ := back[0].Items[0].Ours.AsText(); got != "ours_name" {
		t.Errorf("round-trip ours: %q", got)
	}
}

func TestCreateRebasedChangeset_SameUpdateNoConflict(t *testing.T) {
	base := tmpPath(t, "same_base.sqlite")
	theirs := tmpPath(t, "same_theirs.sqlite")
	ours := tmpPath(t, "same_ours.sqlite")
	baseTheirs := tmpPath(t, "same_base_theirs.diff")
	rebased := tmpPath(t, "same_rebased.diff")
	seedSimple(t, base)
	copyAndExec(t, base, theirs, "UPDATE simple SET name = 'same_name' WHERE fid = 1")
	copyAndExec(t, base, ours, "UPDATE simple SET name = 'same_name' WHERE fid = 1")
	if err := CreateChangeset(base, theirs, baseTheirs); err != nil {
		t.Fatalf("CreateChangeset: %v", err)
	}

	conflicts, err := CreateRebasedChangeset(base, ours, baseTheirs, rebased, "")
	if err != nil {
		t.Fatalf("CreateRebasedChangeset: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("expected 0 conflicts, got %d", len(conflicts))
	}
}

func TestCreateRebasedChangeset_DeleteVsUpdate(t *testing.T) {
	base := tmpPath(t, "del_base.sqlite")
	theirs := tmpPath(t, "del_theirs.sqlite")
	ours := tmpPath(t, "del_ours.sqlite")
	baseTheirs := tmpPath(t, "del_base_theirs.diff")
	rebased := tmpPath(t, "del_rebased.diff")
	seedSimple(t, base)
	copyAndExec(t, base, theirs, "DELETE FROM simple WHERE fid = 1")
	copyAndExec(t, base, ours, "UPDATE simple SET name = 'new_name' WHERE fid = 1")
	if err := CreateChangeset(base, theirs, baseTheirs); err != nil {
		t.Fatalf("CreateChangeset: %v", err)
	}

	conflicts, err := CreateRebasedChangeset(base, ours, baseTheirs, rebased, "")
	if err != nil {
		t.Fatalf("CreateRebasedChangeset: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].PK != 1 || conflicts[0].TableName != "simple" {
		t.Errorf("conflict: %+v", conflicts[0])
	}
	if !conflicts[0].Items[0].Theirs.IsUndefined() {
		t.Errorf("theirs should be undefined (deleted), got %s", conflicts[0].Items[0].Theirs)
	}
}

func TestRebaseChangesets_IsTheDivergePath(t *testing.T) {
	base := tmpPath(t, "rb_base.sqlite")
	theirs := tmpPath(t, "rb_theirs.sqlite")
	ours := tmpPath(t, "rb_ours.sqlite")
	baseTheirs := tmpPath(t, "rb_base_theirs.diff")
	baseOurs := tmpPath(t, "rb_base_ours.diff")
	merged := tmpPath(t, "rb_merged.diff")
	seedSimple(t, base)
	copyAndExec(t, base, theirs, "UPDATE simple SET name = 'theirs_name' WHERE fid = 1")
	copyAndExec(t, base, ours, "UPDATE simple SET name = 'ours_name' WHERE fid = 1")
	if err := CreateChangeset(base, theirs, baseTheirs); err != nil {
		t.Fatalf("base→theirs: %v", err)
	}
	if err := CreateChangeset(base, ours, baseOurs); err != nil {
		t.Fatalf("base→ours: %v", err)
	}

	conflicts, err := RebaseChangesets(baseTheirs, baseOurs, merged)
	if err != nil {
		t.Fatalf("RebaseChangesets: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}

	// Optional sidecar still writes when a path is given.
	sidecar := tmpPath(t, "conflicts.json")
	if _, err := CreateRebasedChangeset(base, ours, baseTheirs, tmpPath(t, "rebased2.diff"), sidecar); err != nil {
		t.Fatalf("sidecar path: %v", err)
	}
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("expected sidecar at %s: %v", sidecar, err)
	}
}
