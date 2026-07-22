package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
	"golang.org/x/crypto/bcrypt"
)

var ErrInvalidCredentials = errors.New("invalid credentials")
var dummyPasswordHash, _ = bcrypt.GenerateFromPassword([]byte("myprobe-dummy-password-comparison"), bcrypt.DefaultCost)

type CaptchaChallenge struct {
	ID        string    `json:"id"`
	Prompt    string    `json:"prompt"`
	ExpiresAt time.Time `json:"expires_at"`
}

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
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password))
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

func (s *Service) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error {
	if len(newPassword) < 12 {
		return errors.New("new password must contain at least 12 characters")
	}
	user, err := s.store.UserByID(ctx, userID)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)) != nil {
		return ErrInvalidCredentials
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(newPassword)) == nil {
		return errors.New("new password must be different")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.store.UpdateUserPassword(ctx, userID, string(hash))
}

func (s *Service) NewCaptcha(ctx context.Context, username, remoteIP string, now time.Time) (CaptchaChallenge, error) {
	left, err := randomDigit()
	if err != nil {
		return CaptchaChallenge{}, err
	}
	right, err := randomDigit()
	if err != nil {
		return CaptchaChallenge{}, err
	}
	id := secureToken(24)
	expires := now.Add(5 * time.Minute)
	if err := s.store.CreateCaptchaChallenge(ctx, id, username, remoteIP, captchaHash(id, fmt.Sprintf("%d", left+right)), expires, now); err != nil {
		return CaptchaChallenge{}, err
	}
	return CaptchaChallenge{ID: id, Prompt: fmt.Sprintf("%d + %d = ?", left, right), ExpiresAt: expires}, nil
}

func (s *Service) VerifyCaptcha(ctx context.Context, id, answer, username, remoteIP string, now time.Time) (bool, error) {
	if id == "" || strings.TrimSpace(answer) == "" {
		return false, nil
	}
	return s.store.ConsumeCaptchaChallenge(ctx, id, username, remoteIP, captchaHash(id, strings.TrimSpace(answer)), now)
}

func randomDigit() (int, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(9))
	if err != nil {
		return 0, err
	}
	return int(value.Int64()) + 1, nil
}

func captchaHash(id, answer string) string {
	sum := sha256.Sum256([]byte(id + ":" + answer))
	return hex.EncodeToString(sum[:])
}

func secureToken(size int) string {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}
