// Package dcs is the Distributed Configuration Store layer — etcd
// today, but the interface stays implementation-agnostic so the
// reconcile loop tests can swap a fake in.
//
// dcs has TWO responsibilities for weft-ha-forgejo (smaller scope
// than weft-ha-postgresql's, because Forgejo replicas are
// stateless once shared secrets are in place — no continuous leader
// election) :
//
//  1. Shared-secret store : the bootstrap leader writes
//     `secret_key` / `internal_token` / `lfs_jwt_secret` under
//     `/weft/forgejo/<install>/secrets/...` ; the other replicas
//     read them on boot and install them in app.ini. The secrets
//     never change after bootstrap (rotating SECRET_KEY would
//     invalidate every session cookie).
//  2. Bootstrap advisory lock : the first replica holds a
//     lease-bound lock and does the Postgres-schema migration +
//     admin-user create + secret minting. The other two wait
//     for the lock to release, then observe the secrets already
//     in place + the install already migrated.
package dcs

import (
	"context"
	"errors"
)

// Store is the minimum surface the reconcile loop needs from a
// distributed configuration store. Implementations are expected to
// be safe for concurrent use.
type Store interface {
	GetKey(ctx context.Context, path string) (string, error)
	PutKeyIfAbsent(ctx context.Context, path, value string) (bool, error)
	AcquireBootstrapLock(ctx context.Context, owner string) (release func(), err error)
	Close() error
}

// ErrNotImplemented signals that a Store method has not been wired
// for the current build.
var ErrNotImplemented = errors.New("dcs: not implemented in scaffold build")

// MemStore is a process-local stand-in for the etcd-backed store.
// Bootstrap-lock acquisition enforces local exclusion only ; key
// set/get persist for the process's lifetime.
type MemStore struct {
	keys   map[string]string
	locked bool
}

// NewMemStore returns an empty MemStore.
func NewMemStore() *MemStore { return &MemStore{keys: map[string]string{}} }

// GetKey implements Store.
func (m *MemStore) GetKey(_ context.Context, path string) (string, error) {
	return m.keys[path], nil
}

// PutKeyIfAbsent implements Store.
func (m *MemStore) PutKeyIfAbsent(_ context.Context, path, value string) (bool, error) {
	if _, ok := m.keys[path]; ok {
		return false, nil
	}
	m.keys[path] = value
	return true, nil
}

// AcquireBootstrapLock implements Store.
func (m *MemStore) AcquireBootstrapLock(_ context.Context, _ string) (func(), error) {
	if m.locked {
		return func() {}, errors.New("memstore bootstrap lock already held in-process")
	}
	m.locked = true
	return func() { m.locked = false }, nil
}

// Close implements Store.
func (m *MemStore) Close() error { return nil }
