// Package team owns human authentication and revocable browser sessions. Shared
// room data remains in storage; a session never owns execution lifetimes.
package team

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

const SessionLifetime = 7 * 24 * time.Hour

type Service struct {
	store         *storage.Store
	setupPath     string
	mu            sync.Mutex
	subscriptions map[string]map[uint64]context.CancelFunc
	next          uint64
	passwordSlots chan struct{}
	dummyHash     string
}

func Open(ctx context.Context, store *storage.Store, dataDir string) (*Service, error) {
	s := &Service{store: store, setupPath: filepath.Join(dataDir, "team-setup.token"), subscriptions: map[string]map[uint64]context.CancelFunc{}, passwordSlots: make(chan struct{}, 2)}
	s.dummyHash = "$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	required, e := store.SetupRequired(ctx)
	if e != nil {
		return nil, e
	}
	if !required {
		if e = os.Remove(s.setupPath); e != nil && !errors.Is(e, os.ErrNotExist) {
			return nil, e
		}
		return s, nil
	}
	if e = os.MkdirAll(dataDir, 0700); e != nil {
		return nil, e
	}
	token, e := randomToken()
	if e != nil {
		return nil, e
	}
	file, e := os.OpenFile(s.setupPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(e, os.ErrExist) {
		info, err := os.Lstat(s.setupPath)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			return nil, errors.New("setup token must be a regular private file with mode 0600")
		}
		b, err := os.ReadFile(s.setupPath)
		if err != nil {
			return nil, err
		}
		token = strings.TrimSpace(string(b))
		if !validToken(token) {
			return nil, errors.New("invalid setup token file")
		}
	} else if e != nil {
		return nil, e
	} else {
		_, e = file.WriteString(token + "\n")
		if e == nil {
			e = file.Sync()
		}
		e = errors.Join(e, file.Close())
		if e != nil {
			return nil, e
		}
	}
	if e = store.EnsureSetupToken(ctx, tokenHash(token)); e != nil {
		return nil, e
	}
	return s, nil
}
func (s *Service) SetupPath() string                               { return s.setupPath }
func (s *Service) SetupRequired(ctx context.Context) (bool, error) { return s.store.SetupRequired(ctx) }
func randomToken() (string, error) {
	bytes := make([]byte, 32)
	_, e := rand.Read(bytes)
	return base64.RawURLEncoding.EncodeToString(bytes), e
}
func validToken(token string) bool {
	v, e := base64.RawURLEncoding.DecodeString(token)
	return e == nil && len(v) == 32
}
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func (s *Service) password(ctx context.Context, password string) (string, error) {
	select {
	case s.passwordSlots <- struct{}{}:
		defer func() { <-s.passwordSlots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	v, e := hashPassword(password)
	if e != nil {
		return "", &storage.Error{Code: "invalid_password", Message: e.Error()}
	}
	return v, nil
}
func (s *Service) Setup(ctx context.Context, token, username, name, password string, teamNames ...string) (storage.User, string, error) {
	if !validToken(token) {
		return storage.User{}, "", &storage.Error{Code: "invalid_credentials", Message: "The setup token is invalid."}
	}
	hash, e := s.password(ctx, password)
	if e != nil {
		return storage.User{}, "", e
	}
	session, e := randomToken()
	if e != nil {
		return storage.User{}, "", e
	}
	u, e := s.store.SetupAdmin(ctx, tokenHash(token), username, name, hash, tokenHash(session), time.Now().Add(SessionLifetime).UTC().Format(time.RFC3339Nano), teamNames...)
	if e == nil {
		_ = os.Remove(s.setupPath)
	}
	return u, session, e
}
func (s *Service) Login(ctx context.Context, username, password string) (storage.User, string, error) {
	u, hash, e := s.store.LoginUser(ctx, username)
	if e != nil {
		hash = s.dummyHash
	}
	select {
	case s.passwordSlots <- struct{}{}:
	case <-ctx.Done():
		return storage.User{}, "", ctx.Err()
	}
	valid := verifyPassword(password, hash)
	<-s.passwordSlots
	if e != nil || !valid || !u.Enabled {
		return storage.User{}, "", &storage.Error{Code: "invalid_credentials", Message: "The username or password is incorrect."}
	}
	token, e := randomToken()
	if e != nil {
		return storage.User{}, "", e
	}
	e = s.store.CreateSession(ctx, tokenHash(token), u.ID, time.Now().Add(SessionLifetime).UTC().Format(time.RFC3339Nano))
	return u, token, e
}
func (s *Service) Authenticate(ctx context.Context, token string) (storage.User, error) {
	if !validToken(token) {
		return storage.User{}, &storage.Error{Code: "unauthenticated", Message: "Sign in to Camp."}
	}
	return s.store.SessionUser(ctx, tokenHash(token))
}
func (s *Service) Logout(ctx context.Context, token string) error {
	hash := tokenHash(token)
	if e := s.store.DeleteSession(ctx, hash); e != nil {
		return e
	}
	s.revoke(hash)
	return nil
}

// WatchSession closes active streams immediately on local revocation. The HTTP
// layer also revalidates periodically for expiry and another process's changes.
func (s *Service) WatchSession(ctx context.Context, token string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	hash := tokenHash(token)
	s.mu.Lock()
	s.next++
	id := s.next
	if s.subscriptions[hash] == nil {
		s.subscriptions[hash] = map[uint64]context.CancelFunc{}
	}
	s.subscriptions[hash][id] = cancel
	s.mu.Unlock()
	return ctx, func() {
		cancel()
		s.mu.Lock()
		delete(s.subscriptions[hash], id)
		if len(s.subscriptions[hash]) == 0 {
			delete(s.subscriptions, hash)
		}
		s.mu.Unlock()
	}
}
func (s *Service) revoke(hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cancel := range s.subscriptions[hash] {
		cancel()
	}
	delete(s.subscriptions, hash)
}
func (s *Service) RevalidateSessions(ctx context.Context) {
	s.mu.Lock()
	hashes := make([]string, 0, len(s.subscriptions))
	for hash := range s.subscriptions {
		hashes = append(hashes, hash)
	}
	s.mu.Unlock()
	for _, hash := range hashes {
		if _, e := s.store.SessionUser(ctx, hash); revokedSession(e) {
			s.revoke(hash)
		}
	}
}
func (s *Service) CreateInvite(ctx context.Context, hours int) (storage.Invite, string, error) {
	if hours == 0 {
		hours = 48
	}
	if hours < 1 || hours > 168 {
		return storage.Invite{}, "", &storage.Error{Code: "invalid_invite", Message: "Invitations expire in 1 to 168 hours."}
	}
	token, e := randomToken()
	if e != nil {
		return storage.Invite{}, "", e
	}
	invite, e := s.store.CreateInvite(ctx, tokenHash(token), time.Now().Add(time.Duration(hours)*time.Hour))
	return invite, token, e
}
func (s *Service) AcceptInvite(ctx context.Context, token, username, name, password string) (storage.User, string, error) {
	if !validToken(token) {
		return storage.User{}, "", &storage.Error{Code: "invalid_invite", Message: "This invitation is invalid or has expired."}
	}
	hash, e := s.password(ctx, password)
	if e != nil {
		return storage.User{}, "", e
	}
	session, e := randomToken()
	if e != nil {
		return storage.User{}, "", e
	}
	u, e := s.store.AcceptInvite(ctx, tokenHash(token), username, name, hash, tokenHash(session), time.Now().Add(SessionLifetime).UTC().Format(time.RFC3339Nano))
	return u, session, e
}
func (s *Service) UpdateUser(ctx context.Context, id string, role *string, enabled *bool) (storage.User, error) {
	u, e := s.store.UpdateUserAccess(ctx, id, role, enabled)
	if e != nil {
		return u, e
	}
	recheck, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	s.RevalidateSessions(recheck)
	return u, nil
}

func revokedSession(err error) bool {
	var rejected *storage.Error
	return errors.As(err, &rejected) && rejected.Code == "unauthenticated"
}
func (s *Service) RevalidateSession(ctx context.Context, token string) {
	hash := tokenHash(token)
	if _, e := s.store.SessionUser(ctx, hash); revokedSession(e) {
		s.revoke(hash)
	}
}
