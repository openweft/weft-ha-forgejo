package config

import (
	"strings"
	"testing"
)

func validConfig() Config {
	return Config{
		NodeName:      "forgejo-1",
		InstallName:   "forgejo-ha-abc",
		DC:            "dc1",
		EtcdEndpoints: []string{"http://etcd:2379"},
		Domain:        "git.example.com",
		AdminUsername: "root",
		AdminPassword: "x",
		AdminEmail:    "root@example.com",
		DBHost:        "pg.weft",
		DBPort:        5432,
		DBUser:        "forgejo",
		DBPassword:    "y",
		APIAddr:       ":3001",
		MetricsAddr:   ":9103",
	}
}

func TestValidate_AcceptsHappyPath(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("happy path failed: %v", err)
	}
}

func TestValidate_RejectsMissingFields(t *testing.T) {
	cases := map[string]func(*Config){
		"node-name":      func(c *Config) { c.NodeName = "" },
		"install-name":   func(c *Config) { c.InstallName = "" },
		"dc":             func(c *Config) { c.DC = "" },
		"etcd":           func(c *Config) { c.EtcdEndpoints = nil },
		"domain":         func(c *Config) { c.Domain = "" },
		"admin-username": func(c *Config) { c.AdminUsername = "" },
		"admin-password": func(c *Config) { c.AdminPassword = "" },
		"admin-email":    func(c *Config) { c.AdminEmail = "" },
		"db-host":        func(c *Config) { c.DBHost = "" },
		"db-port":        func(c *Config) { c.DBPort = 0 },
		"db-user":        func(c *Config) { c.DBUser = "" },
		"db-password":    func(c *Config) { c.DBPassword = "" },
		"api-addr":       func(c *Config) { c.APIAddr = "" },
		"metrics-addr":   func(c *Config) { c.MetricsAddr = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := validConfig()
			mutate(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("expected error for missing %s", name)
			}
			msg := err.Error()
			if !strings.Contains(msg, "must") && !strings.Contains(msg, "required") {
				t.Errorf("error should explain the requirement, got: %v", err)
			}
		})
	}
}

func TestS3Configured(t *testing.T) {
	cases := []struct {
		name string
		c    Config
		want bool
	}{
		{"all-empty", Config{}, false},
		{"endpoint-only", Config{S3Endpoint: "x"}, false},
		{"endpoint+ak", Config{S3Endpoint: "x", S3AccessKey: "ak"}, false},
		{"endpoint+ak+sk", Config{S3Endpoint: "x", S3AccessKey: "ak", S3SecretKey: "sk"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.c.S3Configured() != tc.want {
				t.Errorf("got %v, want %v", tc.c.S3Configured(), tc.want)
			}
		})
	}
}
