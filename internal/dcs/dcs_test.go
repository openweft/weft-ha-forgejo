package dcs

import (
	"context"
	"testing"
)

func TestMemStore_KeyRoundTrip(t *testing.T) {
	s := NewMemStore()
	v, _ := s.GetKey(context.Background(), "secret_key")
	if v != "" {
		t.Errorf("absent key should be empty, got %q", v)
	}
	ok, err := s.PutKeyIfAbsent(context.Background(), "secret_key", "abc")
	if !ok || err != nil {
		t.Fatalf("first PutKeyIfAbsent should succeed, got (%v, %v)", ok, err)
	}
	ok2, _ := s.PutKeyIfAbsent(context.Background(), "secret_key", "xyz")
	if ok2 {
		t.Error("CAS should reject second write")
	}
	v, _ = s.GetKey(context.Background(), "secret_key")
	if v != "abc" {
		t.Errorf("first writer should win, got %q", v)
	}
}

func TestMemStore_BootstrapLockExclusive(t *testing.T) {
	s := NewMemStore()
	rel, err := s.AcquireBootstrapLock(context.Background(), "owner-1")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := s.AcquireBootstrapLock(context.Background(), "owner-2"); err == nil {
		t.Error("second acquire should fail while held")
	}
	rel()
	rel2, err := s.AcquireBootstrapLock(context.Background(), "owner-3")
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	rel2()
}
