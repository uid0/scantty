package cache

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Cache struct {
	db  *sql.DB
	ttl time.Duration
}

func Open(path string, ttl time.Duration) (*Cache, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("cache: mkdir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, fmt.Errorf("cache: open: %w", err)
	}
	c := &Cache{db: db, ttl: ttl}
	if err := c.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return c, nil
}

func (c *Cache) Close() error { return c.db.Close() }

func (c *Cache) init() error {
	_, err := c.db.Exec(`
		CREATE TABLE IF NOT EXISTS resources (
			kind        TEXT NOT NULL,
			id          TEXT NOT NULL,
			body        BLOB NOT NULL,
			etag        TEXT,
			updated_at  INTEGER NOT NULL,
			PRIMARY KEY (kind, id)
		);
		CREATE INDEX IF NOT EXISTS idx_resources_updated ON resources(kind, updated_at);

		CREATE TABLE IF NOT EXISTS lookups (
			code        TEXT PRIMARY KEY,
			kind        TEXT NOT NULL,
			resource_id TEXT NOT NULL,
			updated_at  INTEGER NOT NULL
		);

		CREATE TABLE IF NOT EXISTS pending_actions (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			kind        TEXT NOT NULL,
			payload     BLOB NOT NULL,
			created_at  INTEGER NOT NULL,
			attempts    INTEGER NOT NULL DEFAULT 0,
			last_error  TEXT
		);
	`)
	return err
}

func (c *Cache) Put(kind, id string, body any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("cache: marshal: %w", err)
	}
	_, err = c.db.Exec(
		`INSERT INTO resources (kind, id, body, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(kind, id) DO UPDATE SET body=excluded.body, updated_at=excluded.updated_at`,
		kind, id, buf, time.Now().Unix(),
	)
	return err
}

type Entry struct {
	Kind      string
	ID        string
	Body      []byte
	UpdatedAt time.Time
	Stale     bool
}

func (c *Cache) Get(kind, id string, out any) (*Entry, error) {
	var body []byte
	var updated int64
	err := c.db.QueryRow(
		`SELECT body, updated_at FROM resources WHERE kind = ? AND id = ?`, kind, id,
	).Scan(&body, &updated)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	entry := &Entry{
		Kind:      kind,
		ID:        id,
		Body:      body,
		UpdatedAt: time.Unix(updated, 0),
	}
	entry.Stale = c.ttl > 0 && time.Since(entry.UpdatedAt) > c.ttl
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return entry, fmt.Errorf("cache: unmarshal: %w", err)
		}
	}
	return entry, nil
}

func (c *Cache) List(kind string, out func(id string, body []byte) error) error {
	rows, err := c.db.Query(`SELECT id, body FROM resources WHERE kind = ? ORDER BY id`, kind)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var body []byte
		if err := rows.Scan(&id, &body); err != nil {
			return err
		}
		if err := out(id, body); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (c *Cache) Forget(kind, id string) error {
	_, err := c.db.Exec(`DELETE FROM resources WHERE kind = ? AND id = ?`, kind, id)
	return err
}

func (c *Cache) RememberLookup(code, kind, resourceID string) error {
	_, err := c.db.Exec(
		`INSERT INTO lookups (code, kind, resource_id, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(code) DO UPDATE SET kind=excluded.kind, resource_id=excluded.resource_id, updated_at=excluded.updated_at`,
		code, kind, resourceID, time.Now().Unix(),
	)
	return err
}

type LookupHit struct {
	Code       string
	Kind       string
	ResourceID string
	UpdatedAt  time.Time
}

func (c *Cache) ResolveLookup(code string) (*LookupHit, error) {
	var hit LookupHit
	var updated int64
	err := c.db.QueryRow(
		`SELECT code, kind, resource_id, updated_at FROM lookups WHERE code = ?`, code,
	).Scan(&hit.Code, &hit.Kind, &hit.ResourceID, &updated)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	hit.UpdatedAt = time.Unix(updated, 0)
	return &hit, nil
}

func (c *Cache) QueuePending(kind string, payload any) (int64, error) {
	buf, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("cache: marshal pending: %w", err)
	}
	res, err := c.db.Exec(
		`INSERT INTO pending_actions (kind, payload, created_at) VALUES (?, ?, ?)`,
		kind, buf, time.Now().Unix(),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

type Pending struct {
	ID        int64
	Kind      string
	Payload   []byte
	CreatedAt time.Time
	Attempts  int
	LastError string
}

func (c *Cache) ListPending(kind string) ([]Pending, error) {
	q := `SELECT id, kind, payload, created_at, attempts, COALESCE(last_error, '') FROM pending_actions`
	var rows *sql.Rows
	var err error
	if kind != "" {
		q += ` WHERE kind = ? ORDER BY id`
		rows, err = c.db.Query(q, kind)
	} else {
		q += ` ORDER BY id`
		rows, err = c.db.Query(q)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Pending
	for rows.Next() {
		var p Pending
		var created int64
		if err := rows.Scan(&p.ID, &p.Kind, &p.Payload, &created, &p.Attempts, &p.LastError); err != nil {
			return nil, err
		}
		p.CreatedAt = time.Unix(created, 0)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (c *Cache) MarkPendingFailure(id int64, errMsg string) error {
	_, err := c.db.Exec(
		`UPDATE pending_actions SET attempts = attempts + 1, last_error = ? WHERE id = ?`, errMsg, id,
	)
	return err
}

func (c *Cache) ResolvePending(id int64) error {
	_, err := c.db.Exec(`DELETE FROM pending_actions WHERE id = ?`, id)
	return err
}
