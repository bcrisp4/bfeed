package imgproxy_test

import (
	"context"
	"testing"

	"github.com/bcrisp4/bfeed/internal/core/coretest"
	"github.com/bcrisp4/bfeed/internal/imgproxy"
)

func TestResolveSecretGeneratesOnce(t *testing.T) {
	store := coretest.NewMemStore()
	ctx := context.Background()
	k1, err := imgproxy.ResolveSecret(ctx, store, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(k1) != 32 {
		t.Fatalf("generated key len=%d, want 32", len(k1))
	}
	k2, err := imgproxy.ResolveSecret(ctx, store, "")
	if err != nil {
		t.Fatal(err)
	}
	if string(k1) != string(k2) {
		t.Fatal("key changed across calls — not persisted")
	}
}

func TestResolveSecretEnvOverrides(t *testing.T) {
	store := coretest.NewMemStore()
	k, err := imgproxy.ResolveSecret(context.Background(), store, "envkey")
	if err != nil {
		t.Fatal(err)
	}
	if string(k) != "envkey" {
		t.Fatalf("got %q want envkey", k)
	}
}
