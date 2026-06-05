// Package config holds the static bootstrap configuration for a
// weft-ha-forgejo agent and its validation.
package config

import (
	"fmt"
	"time"
)

// Config is the static bootstrap configuration for one agent. In
// production it's populated from CLI flags fed by the forgejo-ha
// plugin's `env_from` mapping ; one Config per running replica VM.
type Config struct {
	// NodeName uniquely identifies this replica within the install.
	// Used as the bootstrap-lock owner identity in etcd + the
	// /info response body.
	NodeName string
	// InstallName is the logical Forgejo install (e.g. "forgejo-ha-abc").
	// Acts as the etcd prefix for shared secrets.
	InstallName string
	// DC is the failure domain (datacenter / cell) this replica lives in.
	DC string
	// EtcdEndpoints lists the etcd endpoints backing the shared-secret
	// store + bootstrap advisory lock.
	EtcdEndpoints []string

	// Domain is the public hostname Forgejo serves (sets ROOT_URL).
	Domain string

	// Bootstrap admin user. Created on first run if missing.
	AdminUsername string
	AdminPassword string
	AdminEmail    string

	// Catalog Postgres connection.
	DBHost     string
	DBPort     int
	DBName     string
	DBUser     string
	DBPassword string

	// S3 object storage for attachments + LFS. Empty endpoint =
	// local-disk fallback (NOT HA — single-host dev installs only).
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3Bucket    string

	// Shared secrets. Empty = bootstrap leader mints + seeds via etcd ;
	// non-empty = operator-provided ; agent installs verbatim.
	SecretKey     string // SECRET_KEY — master encryption key
	InternalToken string // INTERNAL_TOKEN — internal API auth
	LFSJWTSecret  string // LFS_JWT_SECRET — LFS signing key

	// SMTP URL for password reset. Empty disables mail.
	SMTPURL string

	// APIAddr is the role API listen address (L7 Caddy active probe).
	APIAddr string
	// MetricsAddr is the Prometheus /metrics listen address.
	MetricsAddr string

	// BootstrapTimeout caps how long Acquire waits for the advisory
	// lock on a fresh install. Past this point we log + give up
	// the tick ; the next reconcile retries.
	BootstrapTimeout time.Duration
}

// Validate reports the first problem with c, or nil if it's usable.
func (c Config) Validate() error {
	switch {
	case c.NodeName == "":
		return fmt.Errorf("node-name must not be empty")
	case c.InstallName == "":
		return fmt.Errorf("install-name must not be empty")
	case c.DC == "":
		return fmt.Errorf("dc (failure domain) must not be empty")
	case len(c.EtcdEndpoints) == 0:
		return fmt.Errorf("at least one etcd endpoint is required")
	case c.Domain == "":
		return fmt.Errorf("domain must not be empty")
	case c.AdminUsername == "":
		return fmt.Errorf("admin-username must not be empty")
	case c.AdminPassword == "":
		return fmt.Errorf("admin-password must not be empty")
	case c.AdminEmail == "":
		return fmt.Errorf("admin-email must not be empty")
	case c.DBHost == "":
		return fmt.Errorf("db-host must not be empty")
	case c.DBPort == 0:
		return fmt.Errorf("db-port must be > 0")
	case c.DBUser == "":
		return fmt.Errorf("db-user must not be empty")
	case c.DBPassword == "":
		return fmt.Errorf("db-password must not be empty")
	case c.APIAddr == "":
		return fmt.Errorf("api-addr must not be empty")
	case c.MetricsAddr == "":
		return fmt.Errorf("metrics-addr must not be empty")
	default:
		return nil
	}
}

// S3Configured reports whether the object-storage path is wired.
// When false, Forgejo falls back to local-disk storage — which only
// makes sense for single-host dev installs.
func (c Config) S3Configured() bool {
	return c.S3Endpoint != "" && c.S3AccessKey != "" && c.S3SecretKey != ""
}
