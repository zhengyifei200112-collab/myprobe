package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
	"golang.org/x/crypto/bcrypt"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

type Service struct {
	store      *store.Store
	sessionTTL time.Duration
}

func New(database *store.Store, sessionTTL time.Duration) *Service {
	return &Service{store: database, sessionTTL: sessionTTL}
}

// Bootstrap creates the first administrator. It returns a generated password only
// when no explicit password was provided.
func (s *Service) Bootstrap(ctx context.Context, username, password string) (string, error) {
	count, err := s.store.UserCount(ctx)
	if err != nil || count > 0 {
		return "", err
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return "", errors.New("administrator username is required")
	}
	generated := ""
	if password == "" {
		generated = secureToken(18)
		password = generated
	}
	if len(password) < 12 {
		return "", errors.New("administrator password must contain at least 12 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash administrator password: %w", err)
	}
	_, err = s.store.CreateUser(ctx, username, string(hash))
	return generated, err
}

func (s *Service) Login(ctx context.Context, username, password string) (store.Session, string, error) {
	user, err := s.store.UserByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		return store.Session{}, "", ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return store.Session{}, "", ErrInvalidCredentials
	}
	return s.store.CreateSession(ctx, user.ID, s.sessionTTL)
}

func (s *Service) Session(ctx context.Context, token string) (store.Session, error) {
	return s.store.SessionByToken(ctx, token)
}

func (s *Service) Logout(ctx context.Context, token string) error {
	return s.store.DeleteSession(ctx, token)
}

func secureToken(size int) string {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}
