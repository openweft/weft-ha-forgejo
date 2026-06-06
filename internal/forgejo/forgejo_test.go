package forgejo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPController_HealthzOK(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/api/v1/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"10.1.2"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewHTTPController(srv.URL)
	st, err := c.CheckStatus(context.Background())
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	if !st.Up {
		t.Errorf("Up = false ; want true (Reason=%q)", st.Reason)
	}
	if st.Version != "10.1.2" {
		t.Errorf("Version = %q ; want 10.1.2", st.Version)
	}
}

func TestHTTPController_HealthzUnhealthy(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"unhealthy"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewHTTPController(srv.URL)
	st, err := c.CheckStatus(context.Background())
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	if st.Up {
		t.Error("Up = true on 503 ; want false")
	}
	if st.Reason == "" {
		t.Error("Reason empty on unhealthy ; want a hint")
	}
}

func TestHTTPController_HealthzStatusFieldUnhealthy(t *testing.T) {
	// Forgejo can return 200 with status="unhealthy" during partial outages.
	// The L7 pool must drain such replicas even though HTTP code is 200.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"unhealthy"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewHTTPController(srv.URL)
	st, err := c.CheckStatus(context.Background())
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	if st.Up {
		t.Error("Up = true with status=unhealthy ; want false")
	}
}

// TestHTTPController_HealthzRFCStatusValues pins the IETF Health Check RFC
// vocabulary Forgejo v10 actually returns ("pass" / "warn" / "fail").
// Caught live against a real Forgejo 10.0.3 on 2026-06-06 : the controller
// previously only accepted the legacy "ok" string and flipped every healthy
// Forgejo v10 to Up=false. Regression guard.
func TestHTTPController_HealthzRFCStatusValues(t *testing.T) {
	cases := []struct {
		name   string
		status string
		wantUp bool
	}{
		{"rfc-pass", "pass", true},
		{"rfc-warn-cache-stale", "warn", true},
		{"rfc-fail", "fail", false},
		{"legacy-ok", "ok", true},
		{"legacy-unhealthy", "unhealthy", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/api/healthz", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":"` + tc.status + `"}`))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()
			c := NewHTTPController(srv.URL)
			st, _ := c.CheckStatus(context.Background())
			if st.Up != tc.wantUp {
				t.Errorf("status=%q : Up = %v, want %v (reason=%q)", tc.status, st.Up, tc.wantUp, st.Reason)
			}
		})
	}
}

func TestHTTPController_NetworkDown(t *testing.T) {
	// Point at a port that isn't listening. The dial fails ; the agent
	// should report Up=false with a Reason, NOT bubble the error up.
	c := NewHTTPController("http://127.0.0.1:1")
	st, err := c.CheckStatus(context.Background())
	if err != nil {
		t.Fatalf("CheckStatus returned err=%v ; want nil so reconcile keeps spinning", err)
	}
	if st.Up {
		t.Error("Up = true on dial failure ; want false")
	}
	if st.Reason == "" {
		t.Error("Reason empty on dial failure ; want a hint for ops")
	}
}

func TestHTTPController_VersionFailureDoesNotFlipUp(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	// /api/v1/version returns 500 — should be ignored.
	mux.HandleFunc("/api/v1/version", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewHTTPController(srv.URL)
	st, err := c.CheckStatus(context.Background())
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	if !st.Up {
		t.Error("Up flipped to false on version-endpoint failure ; should stay true")
	}
	if st.Version != "" {
		t.Errorf("Version = %q ; want empty when /version fails", st.Version)
	}
}

func TestNewHTTPControllerDefaults(t *testing.T) {
	c := NewHTTPController("")
	if c.BaseURL != DefaultBaseURL {
		t.Errorf("BaseURL = %q ; want %q", c.BaseURL, DefaultBaseURL)
	}
	if c.Client == nil {
		t.Fatal("Client = nil ; want a configured client")
	}
	if c.Client.Timeout == 0 {
		t.Error("Client.Timeout = 0 ; want a non-zero default")
	}
}
