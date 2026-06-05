// Command weft-ha-forgejo is the Go-native HA operator for Forgejo
// (AGPLv3+, soft-fork of Gitea). One agent runs alongside every
// replica micro-VM and drives :
//
//   - install bootstrap (shared-secret minting + seeding under an
//     etcd advisory lock ; Postgres schema migration + admin user
//     create ; idempotent across replicas),
//   - role API at :3001 for the L7 Caddy in weft-agent to probe,
//   - per-tick health check against the local Forgejo process.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/openweft/weft-ha-forgejo/internal/api"
	"github.com/openweft/weft-ha-forgejo/internal/config"
	"github.com/openweft/weft-ha-forgejo/internal/dcs"
	"github.com/openweft/weft-ha-forgejo/internal/forgejo"
	"github.com/openweft/weft-ha-forgejo/internal/reconcile"
)

// Build metadata, injected via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "weft-ha-forgejo",
		Short:        "Go-native HA operator for Forgejo (Git forge)",
		Long:         "weft-ha-forgejo bootstraps the Forgejo install (shared secrets, schema, admin),\nexposes a role API for the L7 Caddy in weft-agent, and runs a health probe so\nthe upstream pool drains unhealthy replicas.",
		SilenceUsage: true,
	}
	root.AddCommand(versionCmd(), agentCmd())
	return root
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "weft-ha-forgejo %s (commit %s, built %s)\n", version, commit, date)
			return err
		},
	}
}

func agentCmd() *cobra.Command {
	var (
		cfg    config.Config
		period time.Duration
	)
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Run the per-replica HA agent (one per Forgejo instance)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("invalid config: %w", err)
			}
			return runAgent(cmd.Context(), cfg, period)
		},
	}
	f := cmd.Flags()
	f.StringVar(&cfg.NodeName, "node-name", "", "unique replica name within the install")
	f.StringVar(&cfg.InstallName, "install-name", "", "logical Forgejo install (etcd prefix for shared secrets)")
	f.StringVar(&cfg.DC, "dc", "", "failure domain (datacenter / cell)")
	f.StringSliceVar(&cfg.EtcdEndpoints, "etcd", nil, "etcd endpoints (comma-separated)")
	f.StringVar(&cfg.Domain, "domain", "", "public Forgejo domain (ROOT_URL)")
	f.StringVar(&cfg.AdminUsername, "admin-username", "", "bootstrap admin username")
	f.StringVar(&cfg.AdminPassword, "admin-password", "", "bootstrap admin password")
	f.StringVar(&cfg.AdminEmail, "admin-email", "", "bootstrap admin email")
	f.StringVar(&cfg.DBHost, "db-host", "", "catalog Postgres host")
	f.IntVar(&cfg.DBPort, "db-port", 5432, "catalog Postgres port")
	f.StringVar(&cfg.DBName, "db-name", "forgejo", "catalog Postgres database name")
	f.StringVar(&cfg.DBUser, "db-user", "forgejo", "catalog Postgres user")
	f.StringVar(&cfg.DBPassword, "db-password", "", "catalog Postgres password")
	f.StringVar(&cfg.S3Endpoint, "s3-endpoint", "", "S3 endpoint (attachments + LFS) ; empty falls back to local disk (NOT HA)")
	f.StringVar(&cfg.S3AccessKey, "s3-access-key", "", "S3 access-key-id")
	f.StringVar(&cfg.S3SecretKey, "s3-secret-key", "", "S3 secret-access-key")
	f.StringVar(&cfg.S3Bucket, "s3-bucket", "forgejo", "S3 bucket for attachments + LFS")
	f.StringVar(&cfg.SecretKey, "secret-key", "", "SECRET_KEY (empty = mint + seed via etcd)")
	f.StringVar(&cfg.InternalToken, "internal-token", "", "INTERNAL_TOKEN (empty = mint + seed)")
	f.StringVar(&cfg.LFSJWTSecret, "lfs-jwt-secret", "", "LFS_JWT_SECRET (empty = mint + seed)")
	f.StringVar(&cfg.SMTPURL, "smtp-url", "", "outbound SMTP URL (empty disables password reset mail)")
	f.StringVar(&cfg.APIAddr, "api-addr", ":3001", "role API listen address")
	f.StringVar(&cfg.MetricsAddr, "metrics-addr", ":9103", "Prometheus metrics listen address")
	f.DurationVar(&cfg.BootstrapTimeout, "bootstrap-timeout", 30*time.Second, "wait-for-lock timeout during bootstrap")
	f.DurationVar(&period, "reconcile-interval", 5*time.Second, "reconcile loop interval")
	return cmd
}

func runAgent(ctx context.Context, cfg config.Config, period time.Duration) error {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store := dcs.NewMemStore()
	defer func() { _ = store.Close() }()

	// FakeController : the production controller (next milestone)
	// hits /api/healthz on 127.0.0.1:3000 and `forgejo` CLI for
	// admin-user create + schema migration.
	server := &forgejo.FakeController{NextStatus: forgejo.Status{Up: true, Version: "scaffold"}}

	apiSrv := api.New(cfg.APIAddr, cfg.InstallName, cfg.NodeName, cfg.DC)
	if err := apiSrv.Start(); err != nil {
		return fmt.Errorf("starting role API: %w", err)
	}
	defer shutdown(apiSrv)

	log.Info("weft-ha-forgejo agent started",
		"node", cfg.NodeName, "install", cfg.InstallName, "dc", cfg.DC,
		"domain", cfg.Domain, "api", cfg.APIAddr, "metrics", cfg.MetricsAddr,
		"s3_configured", cfg.S3Configured())

	loop := reconcile.New(cfg, store, server, apiSrv, period, log)
	if err := loop.Run(ctx); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

type shutdowner interface {
	Shutdown(context.Context) error
}

func shutdown(s shutdowner) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Shutdown(ctx)
}
