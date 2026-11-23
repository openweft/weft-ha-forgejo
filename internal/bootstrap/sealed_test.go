package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	dcs "github.com/openweft/weft-ha-dcs"
	"github.com/openweft/weft-ha-forgejo/internal/config"
)

// A cluster seeded in plaintext by an older agent is re-sealed on start, the
// nodes still read the same secrets, and nothing readable is left in the store.
func TestLegacyPlaintextClusterIsResealedOnStart(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{InstallName: "i", NodeName: "n"}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	raw := dcs.NewMemStore()
	legacy := map[string]string{"secret_key": "SK-plain", "internal_token": "IT-plain", "lfs_jwt_secret": "LFS-plain"}
	for k, v := range legacy {
		raw.PutKeyIfAbsent(ctx, keyPath("i", k), v)
	}
	sealed, err := dcs.NewSealedStore(raw, strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // idempotent
		r, err := Run(ctx, cfg, sealed, log)
		if err != nil || r.SecretKey != "SK-plain" || r.InternalToken != "IT-plain" || r.LFSJWTSecret != "LFS-plain" {
			t.Fatalf("result %+v, err %v", r, err)
		}
	}
	for k, v := range legacy {
		stored, _ := raw.GetKey(ctx, keyPath("i", k))
		if stored == v || strings.Contains(stored, v) {
			t.Errorf("%s still plaintext in the store", k)
		}
	}
}
