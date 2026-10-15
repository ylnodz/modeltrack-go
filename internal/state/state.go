// Package state is the SQLite-backed model store.
//
// History is permanent: a model is announced only on a genuine transition
// (new, became_free, became_paid, price_change, context_change, deprecated).
// One transaction is written per tracking cycle.
package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ylnodz/modeltrack-go/internal/model"
)

// Event kinds.
const (
	New           = "new"
	BecameFree    = "became_free"
	BecamePaid    = "became_paid"
	PriceChange   = "price_change"
	ContextChange = "context_change"
	Deprecated    = "deprecated"
)

// DeprecationCycles is how many consecutive misses count as deprecation.
const DeprecationCycles = 3

const schema = `
CREATE TABLE IF NOT EXISTS models (
  source TEXT NOT NULL,
  pk TEXT NOT NULL,
  name TEXT, author TEXT, url TEXT,
  is_free INTEGER NOT NULL DEFAULT 0,
  free_note TEXT, context INTEGER,
  modalities TEXT, price_in REAL, price_out REAL,
  description TEXT, extra TEXT,
  first_seen REAL, last_seen REAL, missing_since REAL,
  deprecated_at REAL,
  PRIMARY KEY (source, pk)
);
CREATE TABLE IF NOT EXISTS price_history (
  source TEXT NOT NULL, pk TEXT NOT NULL, ts REAL NOT NULL,
  price_in REAL, price_out REAL, is_free INTEGER
);
CREATE INDEX IF NOT EXISTS idx_price_hist ON price_history (source, pk, ts);
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts REAL NOT NULL, source TEXT, pk TEXT, kind TEXT, detail TEXT
);
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT);
`

// Event is one announce-worthy state transition.
type Event struct {
	Kind   string
	Model  model.Model
	Detail string
	Old    map[string]any
}

// Store is a single-writer SQLite state store.
type Store struct {
	db    *sql.DB
	path  string
	fresh bool
}

// Open creates or opens the database at path.
func Open(path string) (*Store, error) {
	_, statErr := os.Stat(path)
	fresh := os.IsNotExist(statErr)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// Single writer keeps the WAL simple and avoids SQLITE_BUSY.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	s := &Store{db: db, path: path, fresh: fresh}
	if fresh {
		_ = s.SetMeta(context.Background(), "resumed", fmt.Sprint(time.Now().Unix()))
	}
	return s, nil
}

// migrate brings an existing database up to the columns the current binary
// reads. Without it, a DB created by an older binary (or by the Python
// modeltrack, which shares DB_PATH but has no deprecated_at column) keeps its
// old shape: CREATE TABLE IF NOT EXISTS silently no-ops on an existing table,
// so every DiffAndApply then fails with "no such column".
func migrate(db *sql.DB) error {
	required := map[string]map[string]string{
		"models": {
			"deprecated_at": "REAL",
			"missing_since": "REAL",
			"extra":         "TEXT",
			"description":   "TEXT",
		},
		"events": {"detail": "TEXT"},
	}
	for table, cols := range required {
		have, err := columns(db, table)
		if err != nil {
			return err
		}
		if len(have) == 0 {
			continue // table absent; schema just created it
		}
		for col, typ := range cols {
			if have[col] {
				continue
			}
			if _, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, col, typ)); err != nil {
				return fmt.Errorf("add %s.%s: %w", table, col, err)
			}
		}
	}
	return nil
}

// columns reports the existing column names of table, empty if it is absent.
func columns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query("SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// Fresh reports whether the DB file did not exist before Open.
func (s *Store) Fresh() bool { return s.fresh }

// ── reads ─────────────────────────────────────────────────────

// AllModels returns every stored model, optionally filtered by source.
func (s *Store) AllModels(ctx context.Context, source string) ([]model.Model, error) {
	q := "SELECT source, pk, name, author, url, is_free, free_note, context, modalities, price_in, price_out, description, extra FROM models"
	var args []any
	if source != "" {
		q += " WHERE source=?"
		args = append(args, source)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanModels(rows)
}

// FreeModels returns stored free models, optionally filtered by source.
func (s *Store) FreeModels(ctx context.Context, source string) ([]model.Model, error) {
	q := "SELECT source, pk, name, author, url, is_free, free_note, context, modalities, price_in, price_out, description, extra FROM models WHERE is_free=1"
	if source != "" {
		q += " AND source=? ORDER BY name"
	} else {
		q += " ORDER BY source, name"
	}
	rows, err := s.db.QueryContext(ctx, q, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanModels(rows)
}

type Count struct{ Total, Free int }

// Counts returns source -> (total, free).
func (s *Store) Counts(ctx context.Context) (map[string]Count, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT source, COUNT(*), SUM(is_free) FROM models GROUP BY source")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Count{}
	for rows.Next() {
		var src string
		var total int
		var free sql.NullInt64
		if err := rows.Scan(&src, &total, &free); err != nil {
			return nil, err
		}
		out[src] = Count{Total: total, Free: int(free.Int64)}
	}
	return out, rows.Err()
}

// GetMeta reads a meta value ("" when unset).
func (s *Store) GetMeta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key=?", key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// SetMeta writes a meta value.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)", key, value)
	return err
}

// ── diff + persist ────────────────────────────────────────────

// DiffAndApply diffs a cycle's fetch against stored state and persists it in a
// single transaction, returning the events worth announcing.
func (s *Store) DiffAndApply(ctx context.Context, fetched map[string][]model.Model, watchPrice, watchContext map[string]bool) ([]Event, error) {
	now := float64(time.Now().UnixNano()) / 1e9
	var events []Event

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit

	type existingRow struct {
		isFree      bool
		priceIn     sql.NullFloat64
		priceOut    sql.NullFloat64
		contextLen  sql.NullInt64
		firstSeen   sql.NullFloat64
		missingSinc sql.NullFloat64
		deprecated  sql.NullFloat64
	}
	existing := map[string]existingRow{}
	srcPK := map[string][2]string{}

	rows, err := tx.QueryContext(ctx,
		"SELECT source, pk, is_free, price_in, price_out, context, first_seen, missing_since, deprecated_at FROM models")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var src, pk string
		var e existingRow
		if err := rows.Scan(&src, &pk, &e.isFree, &e.priceIn, &e.priceOut, &e.contextLen,
			&e.firstSeen, &e.missingSinc, &e.deprecated); err != nil {
			rows.Close()
			return nil, err
		}
		key := src + ":" + pk
		existing[key] = e
		srcPK[key] = [2]string{src, pk}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	seen := map[string]bool{}

	for source, models := range fetched {
		for _, m := range models {
			key := m.Key()
			seen[key] = true
			row, ok := existing[key]

			if !ok {
				events = append(events, Event{Kind: New, Model: m})
				if err := upsert(ctx, tx, m, now, now); err != nil {
					return nil, err
				}
				if err := pricePoint(ctx, tx, m, now); err != nil {
					return nil, err
				}
				continue
			}

			if m.IsFree && !row.isFree {
				events = append(events, Event{Kind: BecameFree, Model: m, Old: map[string]any{"is_free": false}})
			} else if !m.IsFree && row.isFree {
				events = append(events, Event{Kind: BecamePaid, Model: m, Old: map[string]any{"is_free": true}})
			}

			priceDirty := numericChanged(row.priceIn, m.PriceIn) || numericChanged(row.priceOut, m.PriceOut)

			if watchPrice[source] && priceDirty {
				events = append(events, Event{Kind: PriceChange, Model: m, Old: map[string]any{
					"price_in":  nullFloatVal(row.priceIn),
					"price_out": nullFloatVal(row.priceOut),
				}})
			}

			if watchContext[source] && intChanged(row.contextLen, m.Context) {
				events = append(events, Event{Kind: ContextChange, Model: m, Old: map[string]any{
					"context": nullIntVal(row.contextLen),
				}})
			}

			firstSeen := now
			if row.firstSeen.Valid {
				firstSeen = row.firstSeen.Float64
			}
			if err := upsert(ctx, tx, m, firstSeen, now); err != nil {
				return nil, err
			}
			// Clear any prior deprecation marker: the model is back.
			if row.deprecated.Valid {
				if _, err := tx.ExecContext(ctx,
					"UPDATE models SET deprecated_at=NULL WHERE source=? AND pk=?", m.Source, m.PK); err != nil {
					return nil, err
				}
			}
			// A transition resets the price series and records a point.
			if priceDirty || (m.IsFree != row.isFree) {
				if err := pricePoint(ctx, tx, m, now); err != nil {
					return nil, err
				}
			}
		}
	}

	// Deprecation: missing for DeprecationCycles consecutive cycles, announced once.
	for key, row := range existing {
		if seen[key] {
			continue
		}
		src, pk := srcPK[key][0], srcPK[key][1]
		if !row.missingSinc.Valid {
			if _, err := tx.ExecContext(ctx,
				"UPDATE models SET missing_since=? WHERE source=? AND pk=?", now, src, pk); err != nil {
				return nil, err
			}
			continue
		}
		if row.deprecated.Valid {
			continue // already announced
		}
		cycles := int((now-row.missingSinc.Float64)/1800) + 1
		if cycles >= DeprecationCycles {
			if _, err := tx.ExecContext(ctx,
				"UPDATE models SET deprecated_at=? WHERE source=? AND pk=?", now, src, pk); err != nil {
				return nil, err
			}
			m, err := s.modelByKey(ctx, tx, src, pk)
			if err != nil {
				return nil, err
			}
			events = append(events, Event{Kind: Deprecated, Model: m})
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return events, nil
}

// RecordEvent appends an announced event to the events log.
func (s *Store) RecordEvent(ctx context.Context, e Event) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO events (ts, source, pk, kind, detail) VALUES (?, ?, ?, ?, ?)",
		float64(time.Now().UnixNano())/1e9, e.Model.Source, e.Model.PK, e.Kind, e.Detail)
	return err
}

// Delete removes one stored model. Used by the smoke test to simulate a
// brand-new model appearing on a later cycle.
func (s *Store) Delete(ctx context.Context, source, pk string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM models WHERE source=? AND pk=?", source, pk)
	return err
}

// ── internals ─────────────────────────────────────────────────

const upsertSQL = `
INSERT INTO models
  (source, pk, name, author, url, is_free, free_note, context,
   modalities, price_in, price_out, description, extra,
   first_seen, last_seen, missing_since, deprecated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL)
ON CONFLICT(source, pk) DO UPDATE SET
  name=excluded.name, author=excluded.author, url=excluded.url,
  is_free=excluded.is_free, free_note=excluded.free_note,
  context=excluded.context, modalities=excluded.modalities,
  price_in=excluded.price_in, price_out=excluded.price_out,
  description=excluded.description, extra=excluded.extra,
  last_seen=excluded.last_seen, missing_since=NULL
`

func upsert(ctx context.Context, tx *sql.Tx, m model.Model, firstSeen, lastSeen float64) error {
	_, err := tx.ExecContext(ctx, upsertSQL,
		m.Source, m.PK, m.Name, m.Author, m.URL, boolInt(m.IsFree), nullStr(m.FreeNote),
		nullIntPtr(m.Context), m.ModalitiesString(), nullFloatPtr(m.PriceIn), nullFloatPtr(m.PriceOut),
		m.Description, m.ExtraJSON(), firstSeen, lastSeen)
	return err
}

func pricePoint(ctx context.Context, tx *sql.Tx, m model.Model, now float64) error {
	_, err := tx.ExecContext(ctx,
		"INSERT INTO price_history (source, pk, ts, price_in, price_out, is_free) VALUES (?, ?, ?, ?, ?, ?)",
		m.Source, m.PK, now, nullFloatPtr(m.PriceIn), nullFloatPtr(m.PriceOut), boolInt(m.IsFree))
	return err
}

func (s *Store) modelByKey(ctx context.Context, tx *sql.Tx, src, pk string) (model.Model, error) {
	row := tx.QueryRowContext(ctx,
		"SELECT source, pk, name, author, url, is_free, free_note, context, modalities, price_in, price_out, description, extra FROM models WHERE source=? AND pk=?",
		src, pk)
	return scanModel(row)
}

type scanner interface{ Scan(dest ...any) error }

func scanModel(sc scanner) (model.Model, error) {
	var m model.Model
	var isFree int
	// Text columns are scanned as nullables: a database written by another
	// binary (or by hand) can hold NULLs, and a plain string scan would abort
	// the whole read instead of treating the value as empty.
	var name, author, url, desc sql.NullString
	var freeNote, modalities, extra sql.NullString
	var ctxLen sql.NullInt64
	var pIn, pOut sql.NullFloat64
	if err := sc.Scan(&m.Source, &m.PK, &name, &author, &url, &isFree, &freeNote,
		&ctxLen, &modalities, &pIn, &pOut, &desc, &extra); err != nil {
		return m, err
	}
	m.Name = name.String
	m.Author = author.String
	m.URL = url.String
	m.Description = desc.String
	m.IsFree = isFree == 1
	m.FreeNote = freeNote.String
	if ctxLen.Valid {
		v := int(ctxLen.Int64)
		m.Context = &v
	}
	m.Modalities = splitModalities(modalities.String)
	if pIn.Valid {
		v := pIn.Float64
		m.PriceIn = &v
	}
	if pOut.Valid {
		v := pOut.Float64
		m.PriceOut = &v
	}
	m.Extra = safeJSON(extra.String)
	return m, nil
}

func scanModels(rows *sql.Rows) ([]model.Model, error) {
	var out []model.Model
	for rows.Next() {
		m, err := scanModel(rows)
		if err != nil {
			return nil, err
		}
		if m.Name == "" {
			m.Name = m.PK
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func splitModalities(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []string{"text"}
	}
	return out
}

func safeJSON(raw string) map[string]any {
	if raw == "" || raw == "{}" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// numericChanged treats sub-epsilon noise as unchanged.
func numericChanged(oldV sql.NullFloat64, newV *float64) bool {
	if !oldV.Valid && newV == nil {
		return false
	}
	if !oldV.Valid || newV == nil {
		return true
	}
	diff := oldV.Float64 - *newV
	return diff > 1e-9 || diff < -1e-9
}

func intChanged(oldV sql.NullInt64, newV *int) bool {
	if !oldV.Valid && newV == nil {
		return false
	}
	if !oldV.Valid || newV == nil {
		return true
	}
	return int(oldV.Int64) != *newV
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullIntPtr(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullFloatPtr(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullFloatVal(v sql.NullFloat64) any {
	if !v.Valid {
		return nil
	}
	return v.Float64
}

func nullIntVal(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return int(v.Int64)
}
