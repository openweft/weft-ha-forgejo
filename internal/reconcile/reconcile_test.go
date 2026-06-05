package reconcile

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/openweft/weft-ha-forgejo/internal/api"
	"github.com/openweft/weft-ha-forgejo/internal/config"
	"github.com/openweft/weft-ha-forgejo/internal/dcs"
	"github.com/openweft/weft-ha-forgejo/internal/forgejo"
)

func cfg() config.Config {
	return config.Config{
		NodeName:    "forgejo-1",
		InstallName: "forgejo-ha-abc",
		DC:          "dc1",
	}
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestTick_HealthyMarksAPIUp(t *testing.T) {
	store := dcs.NewMemStore()
	server := &forgejo.FakeController{NextStatus: forgejo.Status{Up: true, Version: "10.0.0"}}
	srv := api.New(":0", "forgejo-ha-abc", "forgejo-1", "dc1")
	l := New(cfg(), store, server, srv, time.Second, discard())
	l.tick(context.Background())
}

func TestTick_DownMarksAPIDown(t *testing.T) {
	store := dcs.NewMemStore()
	server := &forgejo.FakeController{NextStatus: forgejo.Status{Up: false, Reason: "boot in progress"}}
	srv := api.New(":0", "forgejo-ha-abc", "forgejo-1", "dc1")
	l := New(cfg(), store, server, srv, time.Second, discard())
	l.tick(context.Background())
}

func TestRun_ExitsOnContextCancel(t *testing.T) {
	store := dcs.NewMemStore()
	server := &forgejo.FakeController{NextStatus: forgejo.Status{Up: true}}
	srv := api.New(":0", "forgejo-ha-abc", "forgejo-1", "dc1")
	l := New(cfg(), store, server, srv, 10*time.Millisecond, discard())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.Run(ctx); err != context.Canceled {
		t.Errorf("Run should exit with ctx.Err(), got %v", err)
	}
}
