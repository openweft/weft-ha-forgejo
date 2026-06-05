// Package reconcile is the agent's tick loop : bootstrap the install
// once, then on every tick refresh the role API's State view from
// the local Forgejo's health probe.
//
// As with weft-ha-irods, the loop is intentionally small. Forgejo
// replicas don't elect a leader (the catalog Postgres + shared
// secrets do all the coordination), so there's no "promote" /
// "demote" branch — just "am I healthy on the public domain ?".
package reconcile

import (
	"context"
	"log/slog"
	"time"

	"github.com/openweft/weft-ha-forgejo/internal/api"
	"github.com/openweft/weft-ha-forgejo/internal/bootstrap"
	"github.com/openweft/weft-ha-forgejo/internal/config"
	"github.com/openweft/weft-ha-forgejo/internal/dcs"
	"github.com/openweft/weft-ha-forgejo/internal/forgejo"
)

// Loop owns the reconcile state machine.
type Loop struct {
	cfg              config.Config
	store            dcs.Store
	server           forgejo.Controller
	apiSrv           *api.Server
	period           time.Duration
	log              *slog.Logger
	bootstrappedOnce bool
}

// New returns a Loop wired to the given components.
func New(cfg config.Config, store dcs.Store, server forgejo.Controller, apiSrv *api.Server, period time.Duration, log *slog.Logger) *Loop {
	return &Loop{cfg: cfg, store: store, server: server, apiSrv: apiSrv, period: period, log: log}
}

// Run drives the loop. Returns when ctx cancels.
func (l *Loop) Run(ctx context.Context) error {
	l.tick(ctx)
	t := time.NewTicker(l.period)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			l.tick(ctx)
		}
	}
}

func (l *Loop) tick(ctx context.Context) {
	if !l.bootstrappedOnce {
		if _, err := bootstrap.Run(ctx, l.cfg, l.store, l.log); err != nil {
			l.log.Warn("bootstrap failed ; will retry next tick", "err", err)
		} else {
			l.bootstrappedOnce = true
		}
	}

	st, err := l.server.CheckStatus(ctx)
	if err != nil {
		l.log.Warn("Forgejo status probe failed", "err", err)
		l.apiSrv.Update(api.State{InstallName: l.cfg.InstallName, NodeName: l.cfg.NodeName, DC: l.cfg.DC, Up: false})
		return
	}
	l.apiSrv.Update(api.State{
		InstallName: l.cfg.InstallName,
		NodeName:    l.cfg.NodeName,
		DC:          l.cfg.DC,
		Up:          st.Up,
		Version:     st.Version,
	})
}
