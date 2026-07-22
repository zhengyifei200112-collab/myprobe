package sharing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestShareAuthenticationScopeAndSession(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, _, _ := database.CreateNode(ctx, store.CreateNodeParams{Name: "selected"})
	service := New(database, time.Hour)
	share, err := service.Create(ctx, "customer", "long-password", []string{node.ID, node.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(share.NodeIDs) != 1 || share.PasswordHash == "long-password" {
		t.Fatalf("share = %#v", share)
	}
	now := time.Now().UTC()
	session, token, err := service.Login(ctx, share.ID, "long-password", "192.0.2.1", now)
	if err != nil || token == "" || !session.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("session = %#v, token = %q, error = %v", session, token, err)
	}
	authorized, err := service.Authenticate(ctx, share.ID, token, now.Add(time.Minute))
	if err != nil || !service.AllowsNode(authorized, node.ID) || service.AllowsNode(authorized, "other") {
		t.Fatalf("authorized = %#v, error = %v", authorized, err)
	}
	if _, err := service.Authenticate(ctx, share.ID, token, now.Add(2*time.Hour)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired error = %v", err)
	}
}

func TestSharePasswordRateLimitPersistsAndIsIPScoped(t *testing.T) {
	ctx := context.Background()
	database, _ := store.Open(ctx, ":memory:")
	defer database.Close()
	node, _, _ := database.CreateNode(ctx, store.CreateNodeParams{Name: "selected"})
	service := New(database, time.Hour)
	share, _ := service.Create(ctx, "customer", "long-password", []string{node.ID})
	now := time.Now().UTC()
	for attempt := 1; attempt <= 4; attempt++ {
		if _, _, err := service.Login(ctx, share.ID, "wrong-password", "192.0.2.1", now.Add(time.Duration(attempt)*time.Second)); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d error = %v", attempt, err)
		}
	}
	_, _, err := service.Login(ctx, share.ID, "wrong-password", "192.0.2.1", now.Add(5*time.Second))
	var rateError *RateLimitError
	if !errors.As(err, &rateError) || rateError.RetryAfter < 14*time.Minute {
		t.Fatalf("rate error = %#v", err)
	}
	if _, _, err := service.Login(ctx, share.ID, "long-password", "192.0.2.1", now.Add(6*time.Second)); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("blocked correct password error = %v", err)
	}
	if _, token, err := service.Login(ctx, share.ID, "long-password", "192.0.2.2", now.Add(6*time.Second)); err != nil || token == "" {
		t.Fatalf("other IP token = %q, error = %v", token, err)
	}
	serviceAfterRestart := New(database, time.Hour)
	if _, _, err := serviceAfterRestart.Login(ctx, share.ID, "long-password", "192.0.2.1", now.Add(7*time.Second)); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("restart rate error = %v", err)
	}
}

func TestSharePasswordRotationAndDisableInvalidatesAccess(t *testing.T) {
	ctx := context.Background()
	database, _ := store.Open(ctx, ":memory:")
	defer database.Close()
	node, _, _ := database.CreateNode(ctx, store.CreateNodeParams{Name: "selected"})
	service := New(database, time.Hour)
	share, _ := service.Create(ctx, "customer", "old-password", []string{node.ID})
	now := time.Now().UTC()
	_, token, _ := service.Login(ctx, share.ID, "old-password", "192.0.2.1", now)
	newPassword := "new-password"
	updated, err := service.Update(ctx, share.ID, "customer", &newPassword, []string{node.ID}, false)
	if err != nil || updated.Enabled {
		t.Fatalf("updated = %#v, error = %v", updated, err)
	}
	if _, err := service.Authenticate(ctx, share.ID, token, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("disabled session error = %v", err)
	}
	updated, err = service.Update(ctx, share.ID, "customer", nil, []string{node.ID}, true)
	if err != nil || !updated.Enabled {
		t.Fatalf("reenable = %#v, error = %v", updated, err)
	}
	if _, err := service.Authenticate(ctx, share.ID, token, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked session became valid after re-enable: %v", err)
	}
	if _, _, err := service.Login(ctx, share.ID, "old-password", "192.0.2.3", now); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password error = %v", err)
	}
	if _, token, err := service.Login(ctx, share.ID, "new-password", "192.0.2.4", now); err != nil || token == "" {
		t.Fatalf("new password token = %q, error = %v", token, err)
	}
}

func TestDeletingNodeRemovesItFromShareScope(t *testing.T) {
	ctx := context.Background()
	database, _ := store.Open(ctx, ":memory:")
	defer database.Close()
	node, _, _ := database.CreateNode(ctx, store.CreateNodeParams{Name: "selected"})
	service := New(database, time.Hour)
	share, err := service.Create(ctx, "customer", "long-password", []string{node.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteNode(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	share, err = database.ChartShare(ctx, share.ID)
	if err != nil || len(share.NodeIDs) != 0 {
		t.Fatalf("share = %#v, error = %v", share, err)
	}
}
