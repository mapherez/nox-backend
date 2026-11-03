package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	noxauth "github.com/mapherez/nox-auth/client"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Auth state has its own private database. The existing data schema, account
// IDs, keys and ownership remain unchanged and readable by previous images.
type NoXLoginConfig struct{ Issuer, Origin, ClientID, Secret, SocketPath string }

func (s *Store) openNoXLogin(ctx context.Context) error {
	path := filepath.Join(s.dataDir, "nox-login.db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		err = f.Close()
	} else if os.IsExist(err) {
		info, e := os.Lstat(path)
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return errors.New("NoX Login database must be a regular file")
		}
		err = nil
	}
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, query := range []string{
		`PRAGMA busy_timeout=5000`, `PRAGMA journal_mode=WAL`, `PRAGMA synchronous=FULL`,
		`CREATE TABLE IF NOT EXISTS config(id INTEGER PRIMARY KEY CHECK(id=1),body BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS transactions(state TEXT PRIMARY KEY,browser TEXT NOT NULL,body BLOB NOT NULL,expires INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS sessions(token_hash TEXT PRIMARY KEY,user_id TEXT NOT NULL,body BLOB NOT NULL,expires INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS identities(issuer TEXT NOT NULL,subject TEXT NOT NULL,user_id TEXT NOT NULL UNIQUE,email TEXT NOT NULL,manager INTEGER NOT NULL,PRIMARY KEY(issuer,subject))`,
	} {
		if _, err = db.ExecContext(ctx, query); err != nil {
			db.Close()
			return err
		}
	}
	s.noxDB = db
	return nil
}
func (s *Store) NoXLoginConfiguration(ctx context.Context) (NoXLoginConfig, bool, error) {
	var value NoXLoginConfig
	var body []byte
	err := s.noxDB.QueryRowContext(ctx, `SELECT body FROM config WHERE id=1`).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return value, false, nil
	}
	if err != nil {
		return value, false, err
	}
	err = json.Unmarshal(body, &value)
	return value, err == nil, err
}
func (s *Store) SaveNoXLoginConfiguration(ctx context.Context, value NoXLoginConfig) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.noxDB.ExecContext(ctx, `INSERT INTO config VALUES(1,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body`, body)
	return err
}
func (s *Store) SaveNoXTransaction(ctx context.Context, browser string, tx noxauth.Transaction) error {
	if _, err := s.noxDB.ExecContext(ctx, `DELETE FROM transactions WHERE expires<=?`, time.Now().Unix()); err != nil {
		return err
	}
	var count int
	if err := s.noxDB.QueryRowContext(ctx, `SELECT count(*) FROM transactions`).Scan(&count); err != nil {
		return err
	}
	if count >= 1000 {
		return errors.New("too many pending login transactions")
	}
	body, err := json.Marshal(tx)
	if err != nil {
		return err
	}
	_, err = s.noxDB.ExecContext(ctx, `INSERT INTO transactions VALUES(?,?,?,?)`, hashSecret(tx.State), hashSecret(browser), body, tx.Created.Add(10*time.Minute).Unix())
	return err
}
func (s *Store) ConsumeNoXTransaction(ctx context.Context, state, browser string) (noxauth.Transaction, error) {
	var tx noxauth.Transaction
	var body []byte
	err := s.noxDB.QueryRowContext(ctx, `DELETE FROM transactions WHERE state=? AND browser=? AND expires>? RETURNING body`, hashSecret(state), hashSecret(browser), time.Now().Unix()).Scan(&body)
	if err != nil {
		return tx, err
	}
	err = json.Unmarshal(body, &tx)
	return tx, err
}
func (s *Store) CreateNoXSession(ctx context.Context, userID string, c noxauth.Credentials) (string, error) {
	if !time.Now().Before(c.ExpiresAt) {
		return "", ErrForbidden
	}
	token, err := randomID(sessionIDPrefix)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	_, err = s.noxDB.ExecContext(ctx, `INSERT INTO sessions VALUES(?,?,?,?)`, hashSecret(token), userID, body, c.ExpiresAt.Unix())
	return token, err
}
func (s *Store) NoXSession(ctx context.Context, token string) (User, noxauth.Credentials, bool, error) {
	var c noxauth.Credentials
	var userID string
	var body []byte
	var expiry int64
	err := s.noxDB.QueryRowContext(ctx, `SELECT user_id,body,expires FROM sessions WHERE token_hash=?`, hashSecret(token)).Scan(&userID, &body, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, c, false, nil
	}
	if err != nil {
		return User{}, c, false, err
	}
	if expiry <= time.Now().Unix() {
		_ = s.DeleteNoXSession(ctx, token)
		return User{}, c, false, nil
	}
	if err = json.Unmarshal(body, &c); err != nil {
		return User{}, c, false, err
	}
	user, err := s.UserByID(ctx, userID)
	if errors.Is(err, ErrNotFound) || err == nil && user.Status != UserStatusActive {
		_ = s.DeleteNoXSession(ctx, token)
		return User{}, c, false, nil
	}
	return user, c, err == nil, err
}
func (s *Store) RefreshNoXSession(ctx context.Context, token string, c noxauth.Credentials) error {
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.noxDB.ExecContext(ctx, `UPDATE sessions SET body=?,expires=? WHERE token_hash=?`, body, c.ExpiresAt.Unix(), hashSecret(token))
	return err
}
func (s *Store) DeleteNoXSession(ctx context.Context, token string) error {
	_, err := s.noxDB.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, hashSecret(token))
	return err
}
func (s *Store) NoXManager(userID string) bool {
	var manager bool
	err := s.noxDB.QueryRow(`SELECT manager FROM identities WHERE user_id=?`, userID).Scan(&manager)
	return err == nil && manager
}
func (s *Store) CompleteNoXLogin(ctx context.Context, issuer string, identity noxauth.Identity) (User, error) {
	if issuer == "" || identity.Subject == "" || !identity.EmailVerified || !identity.Admin || identity.GoogleSubject == "" || (identity.GoogleIssuer != "https://accounts.google.com" && identity.GoogleIssuer != "accounts.google.com") {
		return User{}, ErrForbidden
	}
	s.noxIdentityMu.Lock()
	defer s.noxIdentityMu.Unlock()
	var user User
	err := s.db.QueryRowContext(ctx, userSelectSQL()+` WHERE google_sub=?`, identity.GoogleSubject).Scan(userScanDest(&user)...)
	if errors.Is(err, sql.ErrNoRows) {
		user, err = s.UserByEmail(ctx, identity.Email)
	}
	if errors.Is(err, ErrNotFound) {
		user, err = s.UpsertAllowedUser(ctx, identity.Email, UserRoleAdmin)
	}
	if err != nil {
		return User{}, err
	}
	if user.GoogleSub != "" && user.GoogleSub != identity.GoogleSubject {
		return User{}, ErrForbidden
	}
	var previousIssuer, previousSubject string
	err = s.noxDB.QueryRowContext(ctx, `SELECT issuer,subject FROM identities WHERE user_id=?`, user.ID).Scan(&previousIssuer, &previousSubject)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return User{}, err
	}
	if err == nil && (previousIssuer != issuer || previousSubject != identity.Subject) {
		return User{}, ErrForbidden
	}
	var boundID string
	err = s.noxDB.QueryRowContext(ctx, `SELECT user_id FROM identities WHERE issuer=? AND subject=?`, issuer, identity.Subject).Scan(&boundID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return User{}, err
	}
	if err == nil && boundID != user.ID {
		if _, e := s.UserByID(ctx, boundID); !errors.Is(e, ErrNotFound) {
			return User{}, ErrForbidden
		}
		if _, err = s.noxDB.ExecContext(ctx, `DELETE FROM identities WHERE issuer=? AND subject=?`, issuer, identity.Subject); err != nil {
			return User{}, err
		}
	}
	first := strings.Fields(identity.Name)
	firstName := ""
	if len(first) > 0 {
		firstName = first[0]
	}
	user, err = s.CompleteGoogleLogin(ctx, OAuthProfile{Sub: identity.GoogleSubject, Email: identity.Email, FirstName: firstName, DisplayName: identity.Name})
	if err != nil {
		return User{}, err
	}
	_, err = s.noxDB.ExecContext(ctx, `INSERT INTO identities VALUES(?,?,?,?,?) ON CONFLICT(issuer,subject) DO UPDATE SET email=excluded.email,manager=excluded.manager`, issuer, identity.Subject, user.ID, user.Email, identity.Admin && user.Role == UserRoleAdmin)
	if err != nil {
		return User{}, fmt.Errorf("bind NoX identity: %w", err)
	}
	return user, nil
}
