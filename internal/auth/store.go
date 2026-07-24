package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrTokenInvalid       = errors.New("token invalid")
	ErrQuotaExceeded      = errors.New("daily quota exceeded")
	ErrIssuanceLimited    = errors.New("token already issued from this IP today")
	ErrCredentialNotFound = errors.New("credential not found")
)

const TokenPrefix = "mt_"

type Store struct {
	db          *sql.DB
	dailyLimit  int
	specialToks map[string]struct{}
}

func tokenForUTCDate(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

// Open initialises the SQLite store at path. The parent directory must be writable.
// dailyLimit is the per-token daily quota; specialTokens are tokens that bypass quota.
func Open(path string, dailyLimit int, specialTokens []string) (*Store, error) {
	if dailyLimit <= 0 {
		dailyLimit = 100
	}
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(4)

	schema := `
CREATE TABLE IF NOT EXISTS tokens (
    token       TEXT PRIMARY KEY,
    created_at  INTEGER NOT NULL,
    created_ip  TEXT,
    daily_limit INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS usage (
    token  TEXT NOT NULL,
    day    TEXT NOT NULL,
    count  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (token, day)
);
CREATE TABLE IF NOT EXISTS issuance (
    ip     TEXT NOT NULL,
    day    TEXT NOT NULL,
    count  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (ip, day)
);
CREATE INDEX IF NOT EXISTS usage_day_idx ON usage(day);

CREATE TABLE IF NOT EXISTS counters (
    day      TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT '',
    resource TEXT NOT NULL DEFAULT '',
    count    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (day, provider, resource)
);
CREATE INDEX IF NOT EXISTS counters_day_idx ON counters(day);

CREATE TABLE IF NOT EXISTS credentials (
    provider   TEXT PRIMARY KEY,
    cookie     TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'unknown',
    detail     TEXT NOT NULL DEFAULT '',
    vip_expiry TEXT NOT NULL DEFAULT '',
    checked_at INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0
);

DROP TABLE IF EXISTS request_log;
`
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}

	special := make(map[string]struct{}, len(specialTokens))
	for _, t := range specialTokens {
		t = strings.TrimSpace(t)
		if t != "" {
			special[t] = struct{}{}
		}
	}
	return &Store{db: db, dailyLimit: dailyLimit, specialToks: special}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) DailyLimit() int { return s.dailyLimit }

// IsSpecial returns true if the token bypasses quota checks.
func (s *Store) IsSpecial(token string) bool {
	if token == "" {
		return false
	}
	_, ok := s.specialToks[token]
	return ok
}

// IssueToken creates a new token attributable to clientIP.
// Throttles to one issuance per IP per UTC day, unless ip == "".
func (s *Store) IssueToken(clientIP string) (string, error) {
	day := tokenForUTCDate(time.Now())
	if clientIP != "" {
		var existing int
		if err := s.db.QueryRow(
			`SELECT count FROM issuance WHERE ip = ? AND day = ?`, clientIP, day,
		).Scan(&existing); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("check issuance: %w", err)
		}
		if existing > 0 {
			return "", ErrIssuanceLimited
		}
	}

	tok, err := generateToken()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC().Unix()

	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`INSERT INTO tokens (token, created_at, created_ip, daily_limit) VALUES (?, ?, ?, ?)`,
		tok, now, clientIP, s.dailyLimit,
	); err != nil {
		return "", fmt.Errorf("insert token: %w", err)
	}
	if clientIP != "" {
		if _, err := tx.Exec(
			`INSERT INTO issuance (ip, day, count) VALUES (?, ?, 1)
             ON CONFLICT(ip, day) DO UPDATE SET count = count + 1`,
			clientIP, day,
		); err != nil {
			return "", fmt.Errorf("record issuance: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return tok, nil
}

// CheckAndConsume verifies the token exists and atomically increments its
// daily usage. Returns (limit, used, remaining) on success.
func (s *Store) CheckAndConsume(token string) (limit int, used int, remaining int, err error) {
	if s.IsSpecial(token) {
		return -1, 0, -1, nil
	}
	var dailyLimit int
	if err := s.db.QueryRow(
		`SELECT daily_limit FROM tokens WHERE token = ?`, token,
	).Scan(&dailyLimit); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, 0, ErrTokenInvalid
		}
		return 0, 0, 0, fmt.Errorf("lookup token: %w", err)
	}
	if dailyLimit <= 0 {
		dailyLimit = s.dailyLimit
	}
	day := tokenForUTCDate(time.Now())

	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, 0, err
	}
	defer tx.Rollback()

	var current int
	if err := tx.QueryRow(
		`SELECT count FROM usage WHERE token = ? AND day = ?`, token, day,
	).Scan(&current); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, 0, fmt.Errorf("read usage: %w", err)
	}
	if current >= dailyLimit {
		return dailyLimit, current, 0, ErrQuotaExceeded
	}
	if _, err := tx.Exec(
		`INSERT INTO usage (token, day, count) VALUES (?, ?, 1)
         ON CONFLICT(token, day) DO UPDATE SET count = count + 1`,
		token, day,
	); err != nil {
		return 0, 0, 0, fmt.Errorf("update usage: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, 0, err
	}
	used = current + 1
	return dailyLimit, used, dailyLimit - used, nil
}

// NextResetUnix returns the unix epoch (seconds) of the next UTC midnight.
func NextResetUnix() int64 {
	now := time.Now().UTC()
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	return next.Unix()
}

// IncrementCounter bumps the counter for (today UTC, provider, resource).
// Empty provider/resource are allowed and stored as ”. Errors are swallowed —
// a missed counter must never break the API call.
func (s *Store) IncrementCounter(provider, resource string) {
	day := tokenForUTCDate(time.Now())
	_, err := s.db.Exec(
		`INSERT INTO counters (day, provider, resource, count) VALUES (?, ?, ?, 1)
         ON CONFLICT(day, provider, resource) DO UPDATE SET count = count + 1`,
		day, provider, resource,
	)
	_ = err
}

// StatsSummary returns request counts over the last `days` UTC days.
// Just totals — no PII, no token-level usage, no per-request rows.
type StatsSummary struct {
	Days       int            `json:"days"`
	Total      int            `json:"total"`
	ByDay      map[string]int `json:"by_day"`
	ByProvider map[string]int `json:"by_provider"`
	ByResource map[string]int `json:"by_resource"`
}

func (s *Store) StatsSummary(days int) (*StatsSummary, error) {
	if days <= 0 {
		days = 7
	}
	cutoff := tokenForUTCDate(time.Now().AddDate(0, 0, -(days - 1)))
	sum := &StatsSummary{
		Days:       days,
		ByDay:      map[string]int{},
		ByProvider: map[string]int{},
		ByResource: map[string]int{},
	}

	if err := s.db.QueryRow(
		`SELECT COALESCE(SUM(count), 0) FROM counters WHERE day >= ?`, cutoff,
	).Scan(&sum.Total); err != nil {
		return nil, err
	}

	queries := []struct {
		dst map[string]int
		sql string
	}{
		{sum.ByDay, `SELECT day, SUM(count) FROM counters WHERE day >= ? GROUP BY day ORDER BY day`},
		{sum.ByProvider, `SELECT provider, SUM(count) FROM counters WHERE day >= ? AND provider != '' GROUP BY provider`},
		{sum.ByResource, `SELECT resource, SUM(count) FROM counters WHERE day >= ? AND resource != '' GROUP BY resource`},
	}
	for _, q := range queries {
		rows, err := s.db.Query(q.sql, cutoff)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err == nil {
				q.dst[k] = n
			}
		}
		rows.Close()
	}
	return sum, nil
}

// CredentialInfo is one provider's stored login state. Cookie is omitted from
// JSON (operator-only secret); the admin UI shows status/expiry, not the raw cookie.
type CredentialInfo struct {
	Provider  string `json:"provider"`
	HasCookie bool   `json:"has_cookie"`
	Status    string `json:"status"`     // alive | expired | invalid | unknown
	Detail    string `json:"detail"`     // free-text, e.g. "VIP 到期 2026-12-01" or error
	VIPExpiry string `json:"vip_expiry"` // YYYY-MM-DD if known
	CheckedAt int64  `json:"checked_at"` // unix seconds of last health probe
	UpdatedAt int64  `json:"updated_at"` // unix seconds the cookie was last set
	Cookie    string `json:"-"`          // never serialised
}

// Cookie returns the stored cookie for a provider, or "" if none. Satisfies the
// meting.Credentials interface so providers can read live cookies at request time.
func (s *Store) Cookie(provider string) string {
	var cookie string
	err := s.db.QueryRow(`SELECT cookie FROM credentials WHERE provider = ?`, provider).Scan(&cookie)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cookie)
}

// SetCredential upserts the cookie for a provider and stamps updated_at. Setting
// a cookie resets health to "unknown" until the next probe.
func (s *Store) SetCredential(provider, cookie string) error {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return errors.New("empty provider")
	}
	now := time.Now().UTC().Unix()
	_, err := s.db.Exec(
		`INSERT INTO credentials (provider, cookie, status, updated_at) VALUES (?, ?, 'unknown', ?)
         ON CONFLICT(provider) DO UPDATE SET cookie = excluded.cookie, status = 'unknown', updated_at = excluded.updated_at`,
		provider, strings.TrimSpace(cookie), now,
	)
	return err
}

// UpdateHealth records the result of a health probe for a provider.
func (s *Store) UpdateHealth(provider, status, detail, vipExpiry string) error {
	now := time.Now().UTC().Unix()
	res, err := s.db.Exec(
		`UPDATE credentials SET status = ?, detail = ?, vip_expiry = ?, checked_at = ? WHERE provider = ?`,
		status, detail, vipExpiry, now, provider,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrCredentialNotFound
	}
	return nil
}

// DeleteCredential removes a provider's stored cookie and status.
func (s *Store) DeleteCredential(provider string) error {
	_, err := s.db.Exec(`DELETE FROM credentials WHERE provider = ?`, provider)
	return err
}

// ListCredentials returns stored state for every provider in `providers`, even
// those with no row yet (reported as status "unknown", has_cookie false).
func (s *Store) ListCredentials(providers []string) ([]CredentialInfo, error) {
	stored := map[string]CredentialInfo{}
	rows, err := s.db.Query(`SELECT provider, cookie, status, detail, vip_expiry, checked_at, updated_at FROM credentials`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c CredentialInfo
		var cookie string
		if err := rows.Scan(&c.Provider, &cookie, &c.Status, &c.Detail, &c.VIPExpiry, &c.CheckedAt, &c.UpdatedAt); err == nil {
			c.HasCookie = strings.TrimSpace(cookie) != ""
			c.Cookie = cookie
			stored[c.Provider] = c
		}
	}
	rows.Close()

	out := make([]CredentialInfo, 0, len(providers))
	seen := map[string]struct{}{}
	for _, p := range providers {
		seen[p] = struct{}{}
		if c, ok := stored[p]; ok {
			out = append(out, c)
		} else {
			out = append(out, CredentialInfo{Provider: p, Status: "unknown"})
		}
	}
	// include any stored providers not in the known list (defensive)
	for p, c := range stored {
		if _, ok := seen[p]; !ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func generateToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return TokenPrefix + hex.EncodeToString(b[:]), nil
}
