package auth

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

var ErrOAuthUnavailable = errors.New("GitHub login is not configured")
var ErrOAuthDenied = errors.New("GitHub account is not allowed")

type GitHubService struct {
	store        *store.Store
	aead         cipher.AEAD
	client       *http.Client
	authorizeURL string
	tokenURL     string
	userURL      string
	sessionTTL   time.Duration
}

func NewGitHubService(database *store.Store, encryptionKey string, sessionTTL time.Duration, client *http.Client) (*GitHubService, error) {
	key := strings.TrimSpace(encryptionKey)
	if len(key) < 32 {
		return nil, errors.New("encryption key must contain at least 32 characters")
	}
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &GitHubService{store: database, aead: aead, client: client, authorizeURL: "https://github.com/login/oauth/authorize", tokenURL: "https://github.com/login/oauth/access_token", userURL: "https://api.github.com/user", sessionTTL: sessionTTL}, nil
}

func (s *GitHubService) PublicStatus(ctx context.Context) (bool, error) {
	settings, err := s.store.GitHubOAuthSettings(ctx)
	if err != nil {
		return false, err
	}
	return settings.Enabled && settings.ClientID != "" && settings.ClientSecretSet && len(settings.UsernameAllowlist) > 0, nil
}

func (s *GitHubService) Settings(ctx context.Context) (store.GitHubOAuthSettings, error) {
	item, err := s.store.GitHubOAuthSettings(ctx)
	item.ClientSecretEncrypted = ""
	return item, err
}

func (s *GitHubService) UpdateSettings(ctx context.Context, enabled bool, clientID, clientSecret, callbackURL string, allowlist []string) (store.GitHubOAuthSettings, error) {
	clientID = strings.TrimSpace(clientID)
	callbackURL = strings.TrimSpace(callbackURL)
	allowlist = normalizeAllowlist(allowlist)
	current, err := s.store.GitHubOAuthSettings(ctx)
	if err != nil {
		return store.GitHubOAuthSettings{}, err
	}
	if enabled && (clientID == "" || (clientSecret == "" && !current.ClientSecretSet) || callbackURL == "" || len(allowlist) == 0) {
		return store.GitHubOAuthSettings{}, errors.New("enabled GitHub login requires Client ID, Client Secret, Callback URL, and at least one allowed username")
	}
	if callbackURL != "" {
		parsed, err := url.Parse(callbackURL)
		if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost"))) {
			return store.GitHubOAuthSettings{}, errors.New("callback URL must use HTTPS, except localhost previews")
		}
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return store.GitHubOAuthSettings{}, errors.New("callback URL cannot contain a query or fragment")
		}
	}
	var encrypted *string
	if clientSecret != "" {
		value, err := s.seal([]byte(clientSecret))
		if err != nil {
			return store.GitHubOAuthSettings{}, err
		}
		encrypted = &value
	}
	item, err := s.store.UpdateGitHubOAuthSettings(ctx, enabled, clientID, encrypted, callbackURL, allowlist)
	item.ClientSecretEncrypted = ""
	return item, err
}

func (s *GitHubService) Begin(ctx context.Context, now time.Time) (string, string, error) {
	settings, err := s.store.GitHubOAuthSettings(ctx)
	if err != nil {
		return "", "", err
	}
	if !settings.Enabled || settings.ClientID == "" || !settings.ClientSecretSet || len(settings.UsernameAllowlist) == 0 {
		return "", "", ErrOAuthUnavailable
	}
	state := secureToken(32)
	if err := s.store.CreateOAuthState(ctx, stateHash(state), "github", now.Add(10*time.Minute), now); err != nil {
		return "", "", err
	}
	values := url.Values{"client_id": {settings.ClientID}, "redirect_uri": {settings.CallbackURL}, "scope": {"read:user"}, "state": {state}}
	return s.authorizeURL + "?" + values.Encode(), state, nil
}

func (s *GitHubService) Complete(ctx context.Context, state, cookieState, code string, now time.Time) (store.Session, string, string, error) {
	if state == "" || cookieState == "" || len(state) != len(cookieState) || subtle.ConstantTimeCompare([]byte(state), []byte(cookieState)) != 1 || code == "" {
		return store.Session{}, "", "", ErrInvalidCredentials
	}
	valid, err := s.store.ConsumeOAuthState(ctx, stateHash(state), "github", now)
	if err != nil || !valid {
		return store.Session{}, "", "", ErrInvalidCredentials
	}
	settings, err := s.store.GitHubOAuthSettings(ctx)
	if err != nil || !settings.Enabled {
		return store.Session{}, "", "", ErrOAuthUnavailable
	}
	secret, err := s.open(settings.ClientSecretEncrypted)
	if err != nil {
		return store.Session{}, "", "", ErrOAuthUnavailable
	}
	payload, _ := json.Marshal(map[string]string{"client_id": settings.ClientID, "client_secret": string(secret), "code": code, "redirect_uri": settings.CallbackURL})
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURL, bytes.NewReader(payload))
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "MyProbe OAuth")
	response, err := s.client.Do(request)
	if err != nil {
		return store.Session{}, "", "", errors.New("GitHub token exchange failed")
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return store.Session{}, "", "", errors.New("GitHub token exchange was rejected")
	}
	var token struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if json.Unmarshal(body, &token) != nil || token.AccessToken == "" || token.Error != "" {
		return store.Session{}, "", "", errors.New("GitHub token response was invalid")
	}
	userRequest, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.userURL, nil)
	userRequest.Header.Set("Authorization", "Bearer "+token.AccessToken)
	userRequest.Header.Set("Accept", "application/vnd.github+json")
	userRequest.Header.Set("User-Agent", "MyProbe OAuth")
	userResponse, err := s.client.Do(userRequest)
	if err != nil {
		return store.Session{}, "", "", errors.New("GitHub user lookup failed")
	}
	defer userResponse.Body.Close()
	userBody, _ := io.ReadAll(io.LimitReader(userResponse.Body, 64<<10))
	var githubUser struct {
		Login string `json:"login"`
	}
	if userResponse.StatusCode < 200 || userResponse.StatusCode >= 300 || json.Unmarshal(userBody, &githubUser) != nil || githubUser.Login == "" {
		return store.Session{}, "", "", errors.New("GitHub user response was invalid")
	}
	allowed := false
	for _, name := range settings.UsernameAllowlist {
		if strings.EqualFold(name, githubUser.Login) {
			allowed = true
			break
		}
	}
	if !allowed {
		return store.Session{}, "", githubUser.Login, ErrOAuthDenied
	}
	admin, err := s.store.FirstUser(ctx)
	if err != nil {
		return store.Session{}, "", "", err
	}
	session, sessionToken, err := s.store.CreateSession(ctx, admin.ID, s.sessionTTL)
	if err == nil {
		_ = s.store.MarkGitHubOAuthVerified(ctx, now)
	}
	return session, sessionToken, githubUser.Login, err
}

func normalizeAllowlist(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		name := strings.ToLower(strings.TrimSpace(value))
		if name != "" && !seen[name] && len(name) <= 39 {
			seen[name] = true
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}
func stateHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func (s *GitHubService) seal(value []byte) (string, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := s.aead.Seal(nonce, nonce, value, []byte("myprobe-github-oauth-v1"))
	return "v1:" + base64.RawURLEncoding.EncodeToString(sealed), nil
}
func (s *GitHubService) open(value string) ([]byte, error) {
	if !strings.HasPrefix(value, "v1:") {
		return nil, errors.New("invalid encrypted OAuth secret")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "v1:"))
	if err != nil || len(raw) < s.aead.NonceSize() {
		return nil, errors.New("invalid encrypted OAuth secret")
	}
	return s.aead.Open(nil, raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():], []byte("myprobe-github-oauth-v1"))
}
