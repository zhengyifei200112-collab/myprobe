package store

import (
	"context"
	"testing"
)

func TestMemoryStoresAreIndependentButConnectionsShareTheirStore(t *testing.T) {
	ctx := context.Background()
	first, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, _, err := first.CreateNode(ctx, CreateNodeParams{Name: "first store"}); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	items, err := second.ListNodes(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("independent memory store inherited %d nodes: %v", len(items), err)
	}
	// Hold one connection to force a second pooled connection for the read.
	connection, err := first.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	items, err = first.ListNodes(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("same-store connections lost shared state: %d %v", len(items), err)
	}
}
