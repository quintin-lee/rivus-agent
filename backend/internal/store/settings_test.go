package store

import (
	"context"
	"testing"
)

func TestSettingsRoundTrip(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := NewSettingsRepo(db)
	ctx := context.Background()
	if err := r.Set(ctx, "model.name", "gpt-4o-mini"); err != nil {
		t.Fatal(err)
	}
	v, ok, err := r.Get(ctx, "model.name")
	if err != nil || !ok || v != "gpt-4o-mini" {
		t.Fatalf("got %q %v %v", v, ok, err)
	}
	if _, ok, _ := r.Get(ctx, "missing"); ok {
		t.Fatal("missing key must return ok=false")
	}
}
