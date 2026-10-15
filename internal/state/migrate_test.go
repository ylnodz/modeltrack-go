package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// pythonSchema is the exact DDL the Python modeltrack wrote. The Go binary
// shares DB_PATH with it, and its models table has no deprecated_at column.
// Without migration, every DiffAndApply fails with "no such column".
const pythonSchema = `
CREATE TABLE IF NOT EXISTS models (
  source TEXT NOT NULL,
  pk TEXT NOT NULL,
  name TEXT, author TEXT, url TEXT,
  is_free INTEGER NOT NULL DEFAULT 0,
  free_note TEXT, context INTEGER,
  modalities TEXT, price_in REAL, price_out REAL,
  description TEXT, extra TEXT,
  first_seen REAL, last_seen REAL, missing_since REAL,
  PRIMARY KEY (source, pk)
);
CREATE TABLE IF NOT EXISTS price_history (
  source TEXT NOT NULL, pk TEXT NOT NULL, ts REAL NOT NULL,
  price_in REAL, price_out REAL, is_free INTEGER
);
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts REAL NOT NULL, source TEXT, pk TEXT, kind TEXT, detail TEXT
);
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT);
`

// TestOpenMigratesPythonDatabase proves a DB created by the Python bot is
// brought forward on Open, preserving its rows, instead of failing every cycle.
func TestOpenMigratesPythonDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "python.db")

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(pythonSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(
		"INSERT INTO models (source, pk, name, is_free, context, first_seen, last_seen) VALUES (?,?,?,?,?,?,?)",
		"openrouter", "kept/model:free", "kept/model:free", 1, 131072, 100.0, 200.0); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("INSERT INTO meta (key, value) VALUES ('seeded','1')"); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a Python-shaped DB must not fail: %v", err)
	}
	defer store.Close()

	// The column DiffAndApply depends on must now exist...
	has, err := columns(store.db, "models")
	if err != nil {
		t.Fatal(err)
	}
	if !has["deprecated_at"] {
		t.Fatal("deprecated_at not added by migration")
	}

	// ...and the pre-existing row must survive.
	models, err := store.AllModels(context.Background(), "openrouter")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].PK != "kept/model:free" {
		t.Fatalf("migrated rows lost: %+v", models)
	}

	// A full diff/apply cycle must now succeed against the migrated schema.
	if _, err := store.DiffAndApply(context.Background(), nil, nil, nil); err != nil {
		t.Fatalf("DiffAndApply after migration: %v", err)
	}

	// The seeded flag is preserved, so a migrated DB is not re-announced.
	v, err := store.GetMeta(context.Background(), "seeded")
	if err != nil {
		t.Fatal(err)
	}
	if v != "1" {
		t.Fatalf("meta lost during migration: %q", v)
	}
}

// TestMigrateIsIdempotent proves reopening an already-migrated DB is a no-op.
func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "twice.db")
	for i := 0; i < 3; i++ {
		store, err := Open(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if _, err := store.DiffAndApply(context.Background(), nil, nil, nil); err != nil {
			t.Fatalf("diff %d: %v", i, err)
		}
		store.Close()
	}
}
