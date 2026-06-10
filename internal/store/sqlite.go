// Package store persists brand-agent session state in SQLite.
//
// We use modernc.org/sqlite (pure-Go, CGO-free) so the binary stays
// single-file portable across Linux flavours without the libc / glibc /
// musl drama of CGO bindings. The schema is intentionally minimal: a
// `sessions` row carries everything needed to resume a multi-turn
// conversation and a `messages` row records each turn for audit and
// model-context replay.
//
// Migrations run on Open() via a simple "user_version" pragma — no
// migration library, no manifest file. New schema versions add a numbered
// step here and bump `currentSchemaVersion`.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const currentSchemaVersion = 1

type Store struct {
	db *sql.DB
}

type Session struct {
	SessionID     string
	SessionStatus string
	MediaBuyID    string
	OfferingID    string
	Placement     string
	Locale        string
	Intent        string
	ConsentGranted bool
	Identity      string // JSON blob
	Capabilities  string // JSON blob
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Message struct {
	SessionID string
	Turn      int
	Role      string // "host" or "brand"
	Content   string
	Payload   string // JSON blob (ui_elements etc.)
	CreatedAt time.Time
}

// Open opens (or creates) the SQLite database at path and runs migrations.
// Pass `:memory:` for ephemeral runs (tests, --simulate-host smoke loops).
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	// SQLite + Go: single connection avoids busy errors on concurrent writers
	// without needing WAL gymnastics. Brand-agent traffic is low enough that
	// this is the right tradeoff; revisit if a session ever needs streaming.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if v >= currentSchemaVersion {
		return nil
	}
	if v < 1 {
		if _, err := s.db.Exec(`
			CREATE TABLE sessions (
				session_id      TEXT PRIMARY KEY,
				session_status  TEXT NOT NULL,
				media_buy_id    TEXT,
				offering_id     TEXT,
				placement       TEXT,
				locale          TEXT,
				intent          TEXT,
				consent_granted INTEGER NOT NULL DEFAULT 0,
				identity_json   TEXT,
				capabilities_json TEXT,
				created_at      TEXT NOT NULL,
				updated_at      TEXT NOT NULL
			);
			CREATE TABLE messages (
				session_id  TEXT NOT NULL REFERENCES sessions(session_id),
				turn        INTEGER NOT NULL,
				role        TEXT NOT NULL,
				content     TEXT NOT NULL,
				payload     TEXT,
				created_at  TEXT NOT NULL,
				PRIMARY KEY (session_id, turn)
			);
		`); err != nil {
			return fmt.Errorf("schema v1: %w", err)
		}
	}
	if _, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", currentSchemaVersion)); err != nil {
		return fmt.Errorf("set user_version: %w", err)
	}
	return nil
}

func (s *Store) CreateSession(ctx context.Context, sess Session) error {
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now().UTC()
	}
	if sess.UpdatedAt.IsZero() {
		sess.UpdatedAt = sess.CreatedAt
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (
			session_id, session_status, media_buy_id, offering_id, placement,
			locale, intent, consent_granted, identity_json, capabilities_json,
			created_at, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		sess.SessionID, sess.SessionStatus, sess.MediaBuyID, sess.OfferingID, sess.Placement,
		sess.Locale, sess.Intent, boolToInt(sess.ConsentGranted), sess.Identity, sess.Capabilities,
		sess.CreatedAt.Format(time.RFC3339Nano), sess.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert session %s: %w", sess.SessionID, err)
	}
	return nil
}

func (s *Store) AppendMessage(ctx context.Context, m Message) error {
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (session_id, turn, role, content, payload, created_at)
		VALUES (?,?,?,?,?,?)`,
		m.SessionID, m.Turn, m.Role, m.Content, m.Payload, m.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert message %s/%d: %w", m.SessionID, m.Turn, err)
	}
	return nil
}

func (s *Store) GetSession(ctx context.Context, id string) (*Session, error) {
	var sess Session
	var consent int
	var createdAt, updatedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT session_id, session_status, media_buy_id, offering_id, placement,
		       locale, intent, consent_granted, identity_json, capabilities_json,
		       created_at, updated_at
		FROM sessions WHERE session_id = ?`, id,
	).Scan(
		&sess.SessionID, &sess.SessionStatus, &sess.MediaBuyID, &sess.OfferingID, &sess.Placement,
		&sess.Locale, &sess.Intent, &consent, &sess.Identity, &sess.Capabilities,
		&createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session %s: %w", id, err)
	}
	sess.ConsentGranted = consent == 1
	sess.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	sess.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &sess, nil
}

// MarshalIdentity is a tiny helper so callers don't import encoding/json
// to stash an Identity blob in the session row.
func MarshalIdentity(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
