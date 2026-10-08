package state

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ylnodz/modeltrack-go/internal/model"
)

func mkModel(pk string, free bool) model.Model {
	return model.Model{Source: "test", PK: pk, Name: pk, IsFree: free}
}

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestNewModelEmitsNew(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	events, err := s.DiffAndApply(ctx, map[string][]model.Model{
		"test": {mkModel("a", true)},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != New {
		t.Fatalf("want one new event, got %+v", events)
	}
	if events[0].Model.PK != "a" {
		t.Fatalf("wrong pk: %s", events[0].Model.PK)
	}
}

func TestNoDuplicateOnUnchanged(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	fetch := map[string][]model.Model{"test": {mkModel("a", true)}}

	if _, err := s.DiffAndApply(ctx, fetch, nil, nil); err != nil {
		t.Fatal(err)
	}
	events, err := s.DiffAndApply(ctx, fetch, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("unchanged cycle emitted %+v", events)
	}
}

func TestBecameFreeAndPaid(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if _, err := s.DiffAndApply(ctx, map[string][]model.Model{"test": {mkModel("a", false)}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	events, err := s.DiffAndApply(ctx, map[string][]model.Model{"test": {mkModel("a", true)}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != BecameFree {
		t.Fatalf("want became_free, got %+v", events)
	}

	events, err = s.DiffAndApply(ctx, map[string][]model.Model{"test": {mkModel("a", false)}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != BecamePaid {
		t.Fatalf("want became_paid, got %+v", events)
	}
}

func TestPriceChange(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	watch := map[string]bool{"test": true}

	m := mkModel("a", false)
	m.PriceIn = f(1.0)
	m.PriceOut = f(2.0)
	if _, err := s.DiffAndApply(ctx, map[string][]model.Model{"test": {m}}, watch, nil); err != nil {
		t.Fatal(err)
	}

	m.PriceOut = f(3.0)
	events, err := s.DiffAndApply(ctx, map[string][]model.Model{"test": {m}}, watch, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != PriceChange {
		t.Fatalf("want price_change, got %+v", events)
	}
}

func TestStateSurvivesAcrossReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "persist.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s1.DiffAndApply(ctx, map[string][]model.Model{"test": {mkModel("a", true)}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening must NOT re-announce: history is permanent.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if s2.Fresh() {
		t.Fatal("reopened store reported fresh")
	}
	events, err := s2.DiffAndApply(ctx, map[string][]model.Model{"test": {mkModel("a", true)}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("reopen re-announced: %+v", events)
	}
}

func TestFreeModelsAndCounts(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if _, err := s.DiffAndApply(ctx, map[string][]model.Model{
		"test": {mkModel("a", true), mkModel("b", false), mkModel("c", true)},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}

	free, err := s.FreeModels(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(free) != 2 {
		t.Fatalf("want 2 free, got %d", len(free))
	}

	counts, err := s.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c := counts["test"]; c.Total != 3 || c.Free != 2 {
		t.Fatalf("bad counts: %+v", c)
	}
}

func TestDeprecationAnnouncedOnce(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	with := map[string][]model.Model{"test": {mkModel("a", true)}}

	if _, err := s.DiffAndApply(ctx, with, nil, nil); err != nil {
		t.Fatal(err)
	}
	// First miss only stamps missing_since.
	events, _ := s.DiffAndApply(ctx, map[string][]model.Model{"test": {}}, nil, nil)
	if len(events) != 0 {
		t.Fatalf("first miss should not announce: %+v", events)
	}
	// Backdate the miss so the cycle threshold is satisfied.
	if _, err := s.db.ExecContext(ctx, "UPDATE models SET missing_since = missing_since - 7200"); err != nil {
		t.Fatal(err)
	}
	events, _ = s.DiffAndApply(ctx, map[string][]model.Model{"test": {}}, nil, nil)
	if len(events) != 1 || events[0].Kind != Deprecated {
		t.Fatalf("want deprecated, got %+v", events)
	}
	// And never again.
	events, _ = s.DiffAndApply(ctx, map[string][]model.Model{"test": {}}, nil, nil)
	if len(events) != 0 {
		t.Fatalf("deprecation re-announced: %+v", events)
	}
}

func f(v float64) *float64 { return &v }
