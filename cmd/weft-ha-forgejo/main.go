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
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	weftslognats "github.com/openweft/weft-slognats"

	"github.com/go-authn/secretsource/secretflag"
	dcs "github.com/openweft/weft-ha-dcs"
	"github.com/openweft/weft-ha-forgejo/internal/api"
	"github.com/openweft/weft-ha-forgejo/internal/config"
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
		cfg     config.Config
		period  time.Duration
		secrets []*secretflag.Binding
	)
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Run the per-replica HA agent (one per Forgejo instance)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := secretflag.Resolve(cmd.Context(), secrets...); err != nil {
				return err
			}
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
	f.StringVar(&cfg.AdminEmail, "admin-email", "", "bootstrap admin email")
	f.StringVar(&cfg.DBHost, "db-host", "", "catalog Postgres host")
	f.IntVar(&cfg.DBPort, "db-port", 5432, "catalog Postgres port")
	f.StringVar(&cfg.DBName, "db-name", "forgejo", "catalog Postgres database name")
	f.StringVar(&cfg.DBUser, "db-user", "forgejo", "catalog Postgres user")
	f.StringVar(&cfg.S3Endpoint, "s3-endpoint", "", "S3 endpoint (attachments + LFS) ; empty falls back to local disk (NOT HA)")
	f.StringVar(&cfg.S3AccessKey, "s3-access-key", "", "S3 access-key-id")
	f.StringVar(&cfg.S3Bucket, "s3-bucket", "forgejo", "S3 bucket for attachments + LFS")
	f.StringVar(&cfg.SMTPURL, "smtp-url", "", "outbound SMTP URL (empty disables password reset mail)")
	f.StringVar(&cfg.APIAddr, "api-addr", ":3001", "role API listen address")
	f.StringVar(&cfg.MetricsAddr, "metrics-addr", ":9103", "Prometheus metrics listen address")
	f.DurationVar(&cfg.BootstrapTimeout, "bootstrap-timeout", 30*time.Second, "wait-for-lock timeout during bootstrap")
	f.DurationVar(&period, "reconcile-interval", 5*time.Second, "reconcile loop interval")
	// Secrets: never a plain value on the command line (visible in ps).
	secrets = []*secretflag.Binding{
		secretflag.Add(cmd, "admin-password", &cfg.AdminPassword, "bootstrap admin password"),
		secretflag.Add(cmd, "db-password", &cfg.DBPassword, "catalog Postgres password"),
		secretflag.Add(cmd, "s3-secret-key", &cfg.S3SecretKey, "S3 secret-access-key"),
		secretflag.Add(cmd, "secret-key", &cfg.SecretKey, "SECRET_KEY (empty = mint + seed via etcd)"),
		secretflag.Add(cmd, "internal-token", &cfg.InternalToken, "INTERNAL_TOKEN (empty = mint + seed)"),
		secretflag.Add(cmd, "lfs-jwt-secret", &cfg.LFSJWTSecret, "LFS_JWT_SECRET (empty = mint + seed)"),
	}
	// The cluster key that seals the shared secrets in etcd (>= 32 bytes,
	// identical on every replica). Rotation: new key here, old one below.
	// These have no plain twin: by reference only (etcd mTLS client key and
	// user password included).
	secrets = append(secrets,
		secretflag.AddFrom(cmd, "etcd-seal-key", &cfg.SealKey, "cluster key sealing the shared secrets in etcd"),
		secretflag.AddFromList(cmd, "etcd-seal-old-key", &cfg.SealOldKeys, "previous cluster key (rotation)"),
		secretflag.AddFrom(cmd, "etcd-client-key", &cfg.EtcdClientKey, "etcd client TLS private key (PEM)"),
		secretflag.AddFrom(cmd, "etcd-password", &cfg.EtcdPassword, "etcd user password"),
	)
	f.StringVar(&cfg.EtcdCA, "etcd-ca", "", "PEM bundle of the CA that signed the etcd servers (default: system roots)")
	f.StringVar(&cfg.EtcdCert, "etcd-cert", "", "etcd client certificate (PEM file) for mutual TLS; needs --etcd-client-key-from")
	f.StringVar(&cfg.EtcdUser, "etcd-user", "", "etcd user name; needs --etcd-password-from")
	f.BoolVar(&cfg.EtcdInsecure, "etcd-insecure-plaintext", false, "allow plaintext etcd (no TLS; a password goes in clear): throwaway development etcd only")
	return cmd
}

func runAgent(ctx context.Context, cfg config.Config, period time.Duration) error {
	log, logCloser := weftslognats.SetupFromEnv("weft.ha.forgejo." + cfg.NodeName + ".log")
	defer logCloser.Close()
	slog.SetDefault(log)
	defer weftslognats.PanicReporter("weft-ha-forgejo")

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := pickStore(cfg, log)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	server := pickController(log)

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

// pickStore selects the DCS backend at runtime.
//
//   - WEFT_HA_FORGEJO_ETCD=host:2379[,host:2379...] → EtcdStore (HA).
//   - unset → MemStore (dev / smoke tests ; NOT cross-replica).
//
// The split here lets the same binary smoke-boot in a single-host dev
// VM (no etcd dep) and also run in production behind a 3-DC etcd
// quorum, without a build-time toggle.
func pickStore(cfg config.Config, log *slog.Logger) (dcs.Store, error) {
	endpoints := envEndpoints("WEFT_HA_FORGEJO_ETCD")
	if len(endpoints) == 0 && len(cfg.EtcdEndpoints) > 0 {
		// Validate() requires at least one --etcd ; honour it as the
		// fallback so a smoke install can stay flag-driven without
		// touching the environment.
		endpoints = cfg.EtcdEndpoints
	}
	if len(endpoints) == 0 {
		log.Warn("DCS = MemStore (no etcd configured) ; NOT cross-replica")
		return dcs.NewMemStore(), nil
	}
	etcd, err := dcs.NewEtcdStore(endpoints, "/weft/forgejo/"+cfg.InstallName, 15, dcs.EtcdSecurity{
		CAFile:         cfg.EtcdCA,
		CertFile:       cfg.EtcdCert,
		KeyPEM:         cfg.EtcdClientKey,
		Username:       cfg.EtcdUser,
		Password:       cfg.EtcdPassword,
		AllowPlaintext: cfg.EtcdInsecure,
	})
	if err != nil {
		return nil, err
	}
	if cfg.EtcdInsecure {
		log.Warn("etcd without TLS (--etcd-insecure-plaintext): anyone on the network can read and modify it")
	}
	log.Info("DCS = etcd (shared secrets sealed)", "endpoints", endpoints, "install", cfg.InstallName,
		"mtls", cfg.EtcdCert != "", "auth", cfg.EtcdUser != "")
	return dcs.NewSealedStore(etcd, cfg.SealKey, cfg.SealOldKeys...)
}

// pickController selects the Forgejo Controller at runtime.
//
//   - WEFT_HA_FORGEJO_USE_REAL_CONTROLLER=1 → HTTPController against
//     the loopback Forgejo on 127.0.0.1:3000.
//   - unset → FakeController returning Up=true (smoke / unit tests).
//
// WEFT_HA_FORGEJO_FORGEJO_URL overrides the base URL when set ; this
// is the seam the CI integration harness uses to point at an httptest
// server.
func pickController(log *slog.Logger) forgejo.Controller {
	if os.Getenv("WEFT_HA_FORGEJO_USE_REAL_CONTROLLER") != "1" {
		log.Warn("Controller = FakeController(Up=true) ; set WEFT_HA_FORGEJO_USE_REAL_CONTROLLER=1 to probe a real Forgejo")
		return &forgejo.FakeController{NextStatus: forgejo.Status{Up: true, Version: "scaffold"}}
	}
	baseURL := os.Getenv("WEFT_HA_FORGEJO_FORGEJO_URL")
	if baseURL == "" {
		baseURL = forgejo.DefaultBaseURL
	}
	log.Info("Controller = HTTPController", "base_url", baseURL)
	return forgejo.NewHTTPController(baseURL)
}

// envEndpoints parses a comma-separated env var into a clean slice
// (empty entries dropped).
func envEndpoints(name string) []string {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}
