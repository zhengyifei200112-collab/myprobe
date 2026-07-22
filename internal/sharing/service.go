package sharing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid share password")
	ErrRateLimited        = errors.New("too many password attempts")
	ErrDisabled           = errors.New("share is disabled")
)

type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string { return ErrRateLimited.Error() }
func (e *RateLimitError) Unwrap() error { return ErrRateLimited }

type Service struct {
	store      *store.Store
	sessionTTL time.Duration
}

func New(database *store.Store, sessionTTL time.Duration) *Service {
	if sessionTTL <= 0 {
		sessionTTL = 12 * time.Hour
	}
	return &Service{store: database, sessionTTL: sessionTTL}
}

func (s *Service) Create(ctx context.Context, name, password string, nodeIDs []string) (store.ChartShare, error) {
	hash, err := hashPassword(password)
	if err != nil {
		return store.ChartShare{}, err
	}
	return s.store.CreateChartShare(ctx, name, hash, nodeIDs)
}

func (s *Service) Update(ctx context.Context, id, name string, password *string, nodeIDs []string, enabled bool) (store.ChartShare, error) {
	var hash *string
	if password != nil && *password != "" {
		value, err := hashPassword(*password)
		if err != nil {
			return store.ChartShare{}, err
		}
		hash = &value
	}
	return s.store.UpdateChartShare(ctx, id, name, hash, nodeIDs, enabled)
}

func (s *Service) Login(ctx context.Context, shareID, password, remoteIP string, now time.Time) (store.ChartShareSession, string, error) {
	key := attemptKey(shareID, remoteIP)
	allowed, retryAfter, err := s.store.ShareLoginAllowed(ctx, key, now)
	if err != nil {
		return store.ChartShareSession{}, "", err
	}
	if !allowed {
		return store.ChartShareSession{}, "", &RateLimitError{RetryAfter: retryAfter}
	}
	share, err := s.store.ChartShare(ctx, shareID)
	if err != nil {
		return store.ChartShareSession{}, "", ErrInvalidCredentials
	}
	if !share.Enabled {
		return store.ChartShareSession{}, "", ErrDisabled
	}
	if bcrypt.CompareHashAndPassword([]byte(share.PasswordHash), []byte(password)) != nil {
		blocked, retry, recordErr := s.store.RecordFailedShareLogin(ctx, key, now)
		if recordErr != nil {
			return store.ChartShareSession{}, "", recordErr
		}
		if blocked {
			return store.ChartShareSession{}, "", &RateLimitError{RetryAfter: retry}
		}
		return store.ChartShareSession{}, "", ErrInvalidCredentials
	}
	if err := s.store.ClearShareLoginAttempts(ctx, key); err != nil {
		return store.ChartShareSession{}, "", err
	}
	return s.store.CreateChartShareSession(ctx, shareID, s.sessionTTL, now)
}

func (s *Service) Authenticate(ctx context.Context, shareID, token string, now time.Time) (store.ChartShare, error) {
	if token == "" {
		return store.ChartShare{}, store.ErrNotFound
	}
	if _, err := s.store.ChartShareSessionByToken(ctx, shareID, token, now); err != nil {
		return store.ChartShare{}, err
	}
	share, err := s.store.ChartShare(ctx, shareID)
	if err != nil || !share.Enabled {
		return store.ChartShare{}, store.ErrNotFound
	}
	return share, nil
}

func (s *Service) AllowsNode(share store.ChartShare, nodeID string) bool {
	for _, id := range share.NodeIDs {
		if id == nodeID {
			return true
		}
	}
	return false
}

func (s *Service) Logout(ctx context.Context, shareID, token string) error {
	return s.store.DeleteChartShareSession(ctx, shareID, token)
}

func hashPassword(password string) (string, error) {
	if len(password) < 8 || len(password) > 256 {
		return "", errors.New("share password must contain between 8 and 256 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash share password: %w", err)
	}
	return string(hash), nil
}

func attemptKey(shareID, remoteIP string) string {
	value := sha256.Sum256([]byte(strings.TrimSpace(shareID) + "|" + strings.TrimSpace(remoteIP)))
	return hex.EncodeToString(value[:])
}
