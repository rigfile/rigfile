package registry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Store is the database layer. Every query is parameterised; nothing is built from user input.
type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

// NewStore wraps a database.
func NewStore(db *sql.DB) *Store { return &Store{DB: db, Now: time.Now} }

func (s *Store) now() time.Time { return s.Now().UTC() }

// Errors the HTTP layer maps to statuses.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	ErrDisabled = errors.New("account disabled")
)

// User is an account.
type User struct {
	ID        int64
	GitHubID  int64
	Login     string
	Name      string
	AvatarURL string
	IsAdmin   bool
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("registry: no randomness: " + err.Error())
	}
	return b
}

func hashOf(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

// ---- users ----------------------------------------------------------------------------------------------------------

// GitHubUser is what GitHub tells us about a signed-in person.
type GitHubUser struct {
	ID        int64
	Login     string
	Name      string
	AvatarURL string
}

// UpsertUser creates the user or refreshes login, name and avatar (a GitHub rename follows the numeric id). isAdmin is
// applied on every sign-in from the configured admin list. A disabled account cannot sign in.
func (s *Store) UpsertUser(ctx context.Context, g GitHubUser, isAdmin bool) (*User, error) {
	login := strings.ToLower(g.Login)
	var u User
	var disabled sql.NullTime
	err := s.DB.QueryRowContext(ctx, `
		INSERT INTO users (github_id, login, name, avatar_url, is_admin) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (github_id) DO UPDATE SET login = EXCLUDED.login, name = EXCLUDED.name, avatar_url = EXCLUDED.avatar_url, is_admin = EXCLUDED.is_admin
		RETURNING id, github_id, login, name, avatar_url, is_admin, disabled_at`,
		g.ID, login, g.Name, g.AvatarURL, isAdmin).Scan(&u.ID, &u.GitHubID, &u.Login, &u.Name, &u.AvatarURL, &u.IsAdmin, &disabled)
	if isReserved(err) {
		return nil, fmt.Errorf("%w: %s", ErrLoginReserved, login)
	}
	if isUnique(err) {
		return nil, fmt.Errorf("%w: %s is already used here by an organisation or another account", ErrNameTaken, login)
	}
	if err != nil {
		return nil, fmt.Errorf("registry: cannot record the account %q: %w", login, err)
	}
	if disabled.Valid {
		return nil, ErrDisabled
	}
	return &u, nil
}

// ErrLoginReserved means the login was vacated by a GitHub rename and still belongs to the account that renamed away from
// it (migration 0005): someone else taking the freed GitHub name must not inherit the rigs' namespace.
var ErrLoginReserved = errors.New("that name is reserved for a renamed account")

// isReserved recognises the refusal raised by the login_reserved() trigger.
func isReserved(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505" && pg.Hint == "login_reserved"
}

// ReleaseLogin frees a reserved login (admin action, for a departed account or a settled dispute). It returns
// ErrNotFound when the login is not reserved.
func (s *Store) ReleaseLogin(ctx context.Context, login string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM login_reservations WHERE login = $1`, strings.ToLower(login))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UserByLogin finds an active user.
func (s *Store) UserByLogin(ctx context.Context, login string) (*User, error) {
	var u User
	err := s.DB.QueryRowContext(ctx, `SELECT id, github_id, login, name, avatar_url, is_admin FROM users WHERE login = $1 AND disabled_at IS NULL`, strings.ToLower(login)).
		Scan(&u.ID, &u.GitHubID, &u.Login, &u.Name, &u.AvatarURL, &u.IsAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &u, err
}

// SetDisabled disables or re-enables an account and ends its sessions and tokens (admin action).
func (s *Store) SetDisabled(ctx context.Context, login string, disabled bool) error {
	var at any
	if disabled {
		at = s.now()
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET disabled_at = $2 WHERE login = $1`, strings.ToLower(login), at)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- web sessions ---------------------------------------------------------------------------------------------------

// CreateSession starts a web session and returns the cookie value (shown once) and the CSRF token bound to it.
func (s *Store) CreateSession(ctx context.Context, userID int64, ttl time.Duration) (cookie, csrf string, err error) {
	cookie = base64.RawURLEncoding.EncodeToString(randomBytes(32))
	secret := randomBytes(32)
	_, err = s.DB.ExecContext(ctx, `INSERT INTO sessions (id_hash, user_id, csrf_secret, created_at, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		hashOf(cookie), userID, secret, s.now(), s.now().Add(ttl))
	return cookie, base64.RawURLEncoding.EncodeToString(secret), err
}

// SessionUser resolves a session cookie: the user and the session's CSRF token. Expired sessions, unknown cookies and
// disabled accounts are ErrNotFound.
func (s *Store) SessionUser(ctx context.Context, cookie string) (*User, string, error) {
	if cookie == "" || len(cookie) > 100 {
		return nil, "", ErrNotFound
	}
	var u User
	var secret []byte
	err := s.DB.QueryRowContext(ctx, `
		SELECT u.id, u.github_id, u.login, u.name, u.avatar_url, u.is_admin, s.csrf_secret
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.id_hash = $1 AND s.expires_at > $2 AND u.disabled_at IS NULL`, hashOf(cookie), s.now()).
		Scan(&u.ID, &u.GitHubID, &u.Login, &u.Name, &u.AvatarURL, &u.IsAdmin, &secret)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	return &u, base64.RawURLEncoding.EncodeToString(secret), nil
}

// DeleteSession ends a session (sign out).
func (s *Store) DeleteSession(ctx context.Context, cookie string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = $1`, hashOf(cookie))
	return err
}

// CSRFEqual compares CSRF tokens in constant time.
func CSRFEqual(a, b string) bool {
	return len(a) > 0 && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// ---- API tokens -----------------------------------------------------------------------------------------------------

const tokenPrefix = "rgf_"

// CreateToken mints an API token. The plain value is returned once; only its hash is stored.
func (s *Store) CreateToken(ctx context.Context, userID int64, name string, ttl time.Duration) (string, error) {
	tok := tokenPrefix + base64.RawURLEncoding.EncodeToString(randomBytes(32))
	_, err := s.DB.ExecContext(ctx, `INSERT INTO api_tokens (token_hash, user_id, name, created_at, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		hashOf(tok), userID, name, s.now(), s.now().Add(ttl))
	return tok, err
}

// TokenUser resolves a bearer token. Unknown, expired, revoked and disabled-account tokens are ErrNotFound.
func (s *Store) TokenUser(ctx context.Context, tok string) (*User, error) {
	if !strings.HasPrefix(tok, tokenPrefix) || len(tok) > 100 {
		return nil, ErrNotFound
	}
	var u User
	err := s.DB.QueryRowContext(ctx, `
		UPDATE api_tokens t SET last_used_at = $2
		FROM users u
		WHERE t.token_hash = $1 AND u.id = t.user_id AND t.revoked_at IS NULL AND t.expires_at > $2 AND u.disabled_at IS NULL
		RETURNING u.id, u.github_id, u.login, u.name, u.avatar_url, u.is_admin`, hashOf(tok), s.now()).
		Scan(&u.ID, &u.GitHubID, &u.Login, &u.Name, &u.AvatarURL, &u.IsAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &u, err
}

// RevokeToken revokes a token (logout).
func (s *Store) RevokeToken(ctx context.Context, tok string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = $2 WHERE token_hash = $1 AND revoked_at IS NULL`, hashOf(tok), s.now())
	return err
}

// ---- device flow (RFC 8628) -----------------------------------------------------------------------------------------

const userCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ" // no vowels, no look-alikes

func newUserCode() string {
	b := randomBytes(8)
	out := make([]byte, 9)
	for i := 0; i < 8; i++ {
		j := i
		if i >= 4 {
			j++
		}
		out[j] = userCodeAlphabet[int(b[i])%len(userCodeAlphabet)]
	}
	out[4] = '-'
	return string(out)
}

// NormalizeUserCode upper-cases and re-inserts the dash so "bcdf ghjk" and "BCDFGHJK" both work.
func NormalizeUserCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if strings.ContainsRune(userCodeAlphabet, r) {
			b.WriteRune(r)
		}
	}
	c := b.String()
	if len(c) != 8 {
		return ""
	}
	return c[:4] + "-" + c[4:]
}

// CreateDevice starts a device sign-in. deviceCode is for the CLI (only its hash is stored), userCode is typed by the
// person on the website.
func (s *Store) CreateDevice(ctx context.Context, ttl time.Duration, interval int) (deviceCode, userCode string, err error) {
	deviceCode = base64.RawURLEncoding.EncodeToString(randomBytes(32))
	for attempt := 0; attempt < 5; attempt++ {
		userCode = newUserCode()
		_, err = s.DB.ExecContext(ctx, `INSERT INTO device_codes (device_code_hash, user_code, created_at, expires_at, interval_s) VALUES ($1, $2, $3, $4, $5)`,
			hashOf(deviceCode), userCode, s.now(), s.now().Add(ttl), interval)
		if err == nil {
			return deviceCode, userCode, nil
		}
	}
	return "", "", err
}

// DecideDevice approves or denies a pending, unexpired user code on behalf of a signed-in user.
func (s *Store) DecideDevice(ctx context.Context, userCode string, userID int64, approve bool) error {
	status := "denied"
	if approve {
		status = "approved"
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE device_codes SET status = $3, user_id = $2 WHERE user_code = $1 AND status = 'pending' AND expires_at > $4`,
		userCode, userID, status, s.now())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeviceExists reports whether the code is pending and unexpired (the /device page checks before showing the form).
func (s *Store) DeviceExists(ctx context.Context, userCode string) bool {
	var ok bool
	_ = s.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM device_codes WHERE user_code = $1 AND status = 'pending' AND expires_at > $2)`, userCode, s.now()).Scan(&ok)
	return ok
}

// DevicePoll is the answer to one poll.
type DevicePoll struct {
	Error    string // authorization_pending | slow_down | access_denied | expired_token | invalid_grant
	Token    string
	Interval int
}

// PollDevice answers the CLI. Polling faster than the interval is slow_down (and the interval grows); an approved code
// yields a token exactly once and is deleted.
func (s *Store) PollDevice(ctx context.Context, deviceCode string, tokenTTL time.Duration) (DevicePoll, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return DevicePoll{}, err
	}
	defer tx.Rollback()
	var (
		status   string
		userID   sql.NullInt64
		expires  time.Time
		interval int
		lastPoll sql.NullTime
	)
	h := hashOf(deviceCode)
	err = tx.QueryRowContext(ctx, `SELECT status, user_id, expires_at, interval_s, last_poll_at FROM device_codes WHERE device_code_hash = $1 FOR UPDATE`, h).
		Scan(&status, &userID, &expires, &interval, &lastPoll)
	if errors.Is(err, sql.ErrNoRows) {
		return DevicePoll{Error: "invalid_grant"}, nil
	}
	if err != nil {
		return DevicePoll{}, err
	}
	now := s.now()
	if !expires.After(now) {
		_, _ = tx.ExecContext(ctx, `DELETE FROM device_codes WHERE device_code_hash = $1`, h)
		return DevicePoll{Error: "expired_token"}, tx.Commit()
	}
	if lastPoll.Valid && now.Sub(lastPoll.Time) < time.Duration(interval)*time.Second {
		interval += 5
		if _, err := tx.ExecContext(ctx, `UPDATE device_codes SET interval_s = $2, last_poll_at = $3 WHERE device_code_hash = $1`, h, interval, now); err != nil {
			return DevicePoll{}, err
		}
		return DevicePoll{Error: "slow_down", Interval: interval}, tx.Commit()
	}
	switch status {
	case "pending":
		if _, err := tx.ExecContext(ctx, `UPDATE device_codes SET last_poll_at = $2 WHERE device_code_hash = $1`, h, now); err != nil {
			return DevicePoll{}, err
		}
		return DevicePoll{Error: "authorization_pending", Interval: interval}, tx.Commit()
	case "denied":
		_, _ = tx.ExecContext(ctx, `DELETE FROM device_codes WHERE device_code_hash = $1`, h)
		return DevicePoll{Error: "access_denied"}, tx.Commit()
	}
	// approved: mint the token in the same transaction, then burn the code
	tok := tokenPrefix + base64.RawURLEncoding.EncodeToString(randomBytes(32))
	if _, err := tx.ExecContext(ctx, `INSERT INTO api_tokens (token_hash, user_id, name, created_at, expires_at) VALUES ($1, $2, 'cli', $3, $4)`,
		hashOf(tok), userID.Int64, now, now.Add(tokenTTL)); err != nil {
		return DevicePoll{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM device_codes WHERE device_code_hash = $1`, h); err != nil {
		return DevicePoll{}, err
	}
	return DevicePoll{Token: tok}, tx.Commit()
}

// ---- audit ----------------------------------------------------------------------------------------------------------

// Audit records who did what. detail must not contain secrets (callers pass ids, versions, reasons).
func (s *Store) Audit(ctx context.Context, actor *User, action, target string, detail any) {
	b, _ := json.Marshal(detail)
	if len(b) == 0 || string(b) == "null" {
		b = []byte("{}")
	}
	var id any
	login := ""
	if actor != nil {
		id, login = actor.ID, actor.Login
	}
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO audit_log (at, actor_id, actor_login, action, target, detail) VALUES ($1, $2, $3, $4, $5, $6)`, s.now(), id, login, action, target, string(b))
}
