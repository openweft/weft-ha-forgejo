package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/openweft/weft-ha-forgejo/internal/config"
	"github.com/openweft/weft-ha-forgejo/internal/dcs"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func cfg() config.Config {
	return config.Config{NodeName: "forgejo-1", InstallName: "forgejo-ha-abc"}
}

func TestRun_LeaderMintsSecretsAndSeedsDCS(t *testing.T) {
	store := dcs.NewMemStore()
	r, err := Run(context.Background(), cfg(), store, discard())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !r.LeaderRanFirstBootstrap {
		t.Error("first invocation on empty DCS should report leader=true")
	}
	if len(r.SecretKey) != 64 || len(r.InternalToken) != 128 || len(r.LFSJWTSecret) != 64 {
		t.Errorf("minted secret lengths : SK=%d, IT=%d, LFS=%d", len(r.SecretKey), len(r.InternalToken), len(r.LFSJWTSecret))
	}
	got, _ := store.GetKey(context.Background(), keyPath("forgejo-ha-abc", "secret_key"))
	if got != r.SecretKey {
		t.Error("DCS secret_key mismatch")
	}
}

func TestRun_FollowerReadsExistingSecrets(t *testing.T) {
	store := dcs.NewMemStore()
	_, _ = store.PutKeyIfAbsent(context.Background(), keyPath("forgejo-ha-abc", "secret_key"), "sk")
	_, _ = store.PutKeyIfAbsent(context.Background(), keyPath("forgejo-ha-abc", "internal_token"), "it")
	_, _ = store.PutKeyIfAbsent(context.Background(), keyPath("forgejo-ha-abc", "lfs_jwt_secret"), "lfs")

	r, err := Run(context.Background(), cfg(), store, discard())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.LeaderRanFirstBootstrap {
		t.Error("follower should not report leader=true")
	}
	if r.SecretKey != "sk" || r.InternalToken != "it" || r.LFSJWTSecret != "lfs" {
		t.Errorf("follower should read peer secrets verbatim, got %+v", r)
	}
}

func TestRun_OperatorOverrideHonoured(t *testing.T) {
	store := dcs.NewMemStore()
	c := cfg()
	c.SecretKey = "op-sk"
	c.InternalToken = "op-it"
	c.LFSJWTSecret = "op-lfs"
	r, err := Run(context.Background(), c, store, discard())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.SecretKey != "op-sk" || r.InternalToken != "op-it" || r.LFSJWTSecret != "op-lfs" {
		t.Errorf("operator overrides should win, got %+v", r)
	}
}

func TestRun_Idempotent(t *testing.T) {
	store := dcs.NewMemStore()
	r1, _ := Run(context.Background(), cfg(), store, discard())
	r2, _ := Run(context.Background(), cfg(), store, discard())
	if r1.SecretKey != r2.SecretKey {
		t.Error("idempotent Run must observe the same secret_key on every tick")
	}
	if r2.LeaderRanFirstBootstrap {
		t.Error("second Run should not report leader=true")
	}
}
