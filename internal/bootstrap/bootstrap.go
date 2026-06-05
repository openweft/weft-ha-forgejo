// Package bootstrap handles the one-shot Forgejo install : shared-secret
// minting + seeding into the DCS so the other two replicas join an
// already-initialised install instead of racing to mint their own
// (which would split SECRET_KEY and corrupt every session cookie).
//
// The Postgres schema migration + admin-user create steps run under
// the same advisory lock. Forgejo's own `forgejo migrate` is
// idempotent, so the followers re-running it is a no-op ; we only
// gate the secret-mint step on the lock.
package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/openweft/weft-ha-forgejo/internal/config"
	"github.com/openweft/weft-ha-forgejo/internal/dcs"
)

// Result is what Run reports back to the reconcile loop. The agent
// renders these three values into /etc/forgejo/app.ini once
// they've been resolved (either minted here or read from DCS).
type Result struct {
	SecretKey     string
	InternalToken string
	LFSJWTSecret  string
	// LeaderRanFirstBootstrap is true when THIS replica minted the
	// secrets + migrated the schema. False means we observed an
	// already-bootstrapped install.
	LeaderRanFirstBootstrap bool
}

// keyPath returns the DCS path for a named shared secret.
func keyPath(install, name string) string {
	return fmt.Sprintf("/weft/forgejo/%s/secrets/%s", install, name)
}

// Run executes the one-shot install bootstrap. Idempotent : safe to
// call every reconcile tick — once the secrets are in DCS,
// subsequent calls short-circuit to a key read.
func Run(ctx context.Context, cfg config.Config, store dcs.Store, log *slog.Logger) (Result, error) {
	existing, err := store.GetKey(ctx, keyPath(cfg.InstallName, "secret_key"))
	if err != nil {
		return Result{}, fmt.Errorf("dcs GetKey(secret_key): %w", err)
	}
	if existing != "" {
		return readExistingSecrets(ctx, cfg, store)
	}

	release, err := store.AcquireBootstrapLock(ctx, cfg.NodeName)
	if err != nil {
		return Result{}, fmt.Errorf("acquire bootstrap lock: %w", err)
	}
	defer release()

	// Re-check under the lock — a peer may have raced us.
	existing, err = store.GetKey(ctx, keyPath(cfg.InstallName, "secret_key"))
	if err != nil {
		return Result{}, fmt.Errorf("dcs GetKey under lock: %w", err)
	}
	if existing != "" {
		log.Info("bootstrap : peer minted secrets between pre-check and lock — joining as follower")
		return readExistingSecrets(ctx, cfg, store)
	}

	log.Info("bootstrap : minting Forgejo shared secrets", "install", cfg.InstallName)
	r, err := mintAndStore(ctx, cfg, store)
	if err != nil {
		return Result{}, err
	}
	r.LeaderRanFirstBootstrap = true
	return r, nil
}

func readExistingSecrets(ctx context.Context, cfg config.Config, store dcs.Store) (Result, error) {
	sk, err := store.GetKey(ctx, keyPath(cfg.InstallName, "secret_key"))
	if err != nil {
		return Result{}, fmt.Errorf("dcs GetKey(secret_key): %w", err)
	}
	it, err := store.GetKey(ctx, keyPath(cfg.InstallName, "internal_token"))
	if err != nil {
		return Result{}, fmt.Errorf("dcs GetKey(internal_token): %w", err)
	}
	lfs, err := store.GetKey(ctx, keyPath(cfg.InstallName, "lfs_jwt_secret"))
	if err != nil {
		return Result{}, fmt.Errorf("dcs GetKey(lfs_jwt_secret): %w", err)
	}
	return Result{
		SecretKey:     sk,
		InternalToken: it,
		LFSJWTSecret:  lfs,
	}, nil
}

// mintAndStore generates the three shared secrets, honouring operator
// overrides from Config, and writes them to DCS under the bootstrap
// lock. SECRET_KEY is 64 hex chars (32 random bytes) — Forgejo
// accepts any length but 32 bytes matches its docs.
func mintAndStore(ctx context.Context, cfg config.Config, store dcs.Store) (Result, error) {
	pick := func(operatorProvided string, nbytes int) (string, error) {
		if operatorProvided != "" {
			return operatorProvided, nil
		}
		b := make([]byte, nbytes)
		if _, err := rand.Read(b); err != nil {
			return "", fmt.Errorf("crypto/rand for shared secret: %w", err)
		}
		return hex.EncodeToString(b), nil
	}
	sk, err := pick(cfg.SecretKey, 32)
	if err != nil {
		return Result{}, err
	}
	it, err := pick(cfg.InternalToken, 64)
	if err != nil {
		return Result{}, err
	}
	lfs, err := pick(cfg.LFSJWTSecret, 32)
	if err != nil {
		return Result{}, err
	}
	for _, kv := range []struct{ k, v string }{
		{"secret_key", sk},
		{"internal_token", it},
		{"lfs_jwt_secret", lfs},
	} {
		if _, err := store.PutKeyIfAbsent(ctx, keyPath(cfg.InstallName, kv.k), kv.v); err != nil {
			return Result{}, fmt.Errorf("dcs PutKeyIfAbsent(%s): %w", kv.k, err)
		}
	}
	return Result{
		SecretKey:     sk,
		InternalToken: it,
		LFSJWTSecret:  lfs,
	}, nil
}
