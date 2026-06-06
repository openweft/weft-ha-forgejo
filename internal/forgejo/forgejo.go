// Package forgejo is the thin shell around the local Forgejo process :
// status probe via /api/healthz, app.ini rendering, on-demand reload
// (`SIGHUP`). The reconcile loop talks to this package rather than
// shelling out directly so unit tests can swap a fake.
package forgejo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Status is the result of a local Forgejo health check.
type Status struct {
	// Up is true when /api/healthz returns 200.
	Up bool
	// Version is what the local Forgejo reports under
	// /api/v1/version (informational ; used for ops dashboards).
	Version string
	// Reason carries the first non-Up signal if Up = false.
	Reason string
}

// Controller is the surface the reconcile loop uses.
type Controller interface {
	// CheckStatus hits /api/healthz on the local Forgejo and returns
	// the freshest Status snapshot. Should be cheap.
	CheckStatus(ctx context.Context) (Status, error)
}

// ErrNotImplemented signals a path that doesn't have an in-process
// fake.
var ErrNotImplemented = errors.New("forgejo: not implemented in scaffold build")

// FakeController is a test double : returns whatever Status the test
// pre-loaded into it. Used in the reconcile-loop unit tests.
type FakeController struct {
	NextStatus Status
	NextErr    error
}

// CheckStatus implements Controller.
func (f *FakeController) CheckStatus(_ context.Context) (Status, error) {
	if f.NextErr != nil {
		return Status{}, f.NextErr
	}
	return f.NextStatus, nil
}

// DefaultBaseURL is the loopback Forgejo URL the agent probes when no
// override is set (forgejo runs in the same micro-VM as the agent and
// listens on 127.0.0.1:3000 by default).
const DefaultBaseURL = "http://127.0.0.1:3000"

// HTTPController is the production Controller : it talks to a real
// Forgejo over HTTP on the loopback interface. It is intentionally
// retry-free — the reconcile loop ticks every ~5s so transient
// failures naturally fold into the next tick.
type HTTPController struct {
	// BaseURL is the Forgejo origin to probe ; usually
	// "http://127.0.0.1:3000".
	BaseURL string
	// Client is the HTTP client used for probes. A 5-second timeout is
	// applied if zero.
	Client *http.Client
}

// NewHTTPController returns a Controller that probes a real Forgejo at
// baseURL. Passing an empty baseURL falls back to DefaultBaseURL. The
// client uses a 5-second timeout — enough to ride out a GC pause in
// Forgejo, short enough that a frozen instance falls out of the L7
// pool within one reconcile tick.
func NewHTTPController(baseURL string) *HTTPController {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &HTTPController{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Client:  &http.Client{Timeout: 5 * time.Second},
	}
}

// healthzResponse mirrors the public shape of Forgejo's /api/healthz
// body. Only `status` is load-bearing today ; the rest is best-effort
// diagnostic.
type healthzResponse struct {
	Status string `json:"status"`
}

// versionResponse mirrors /api/v1/version.
type versionResponse struct {
	Version string `json:"version"`
}

// CheckStatus probes /api/healthz, then best-effort enriches the
// Status with the version reported by /api/v1/version. A
// /api/v1/version failure does NOT flip Up to false — the L7 pool
// cares about /healthz only ; the version field is informational.
func (h *HTTPController) CheckStatus(ctx context.Context) (Status, error) {
	st, err := h.checkHealthz(ctx)
	if err != nil {
		return st, err
	}
	// Version is best-effort ; ignore errors.
	if v, err := h.fetchVersion(ctx); err == nil {
		st.Version = v
	}
	return st, nil
}

func (h *HTTPController) checkHealthz(ctx context.Context) (Status, error) {
	url := h.BaseURL + "/api/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Status{}, fmt.Errorf("build healthz request: %w", err)
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return Status{Up: false, Reason: err.Error()}, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return Status{
			Up:     false,
			Reason: fmt.Sprintf("healthz HTTP %d", resp.StatusCode),
		}, nil
	}
	var parsed healthzResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		// Forgejo returned 200 but a body we can't parse ; treat as up.
		return Status{Up: true}, nil
	}
	// Forgejo v10 follows the IETF Health Check RFC draft : "pass" / "warn"
	// / "fail" rather than the legacy "ok" / "unhealthy" pair. Accept both
	// so the controller stays usable against older Gitea-era binaries and
	// the current Forgejo line. "warn" surfaces as Up=true because the
	// health endpoint is composite (cache + database pings) and a stale
	// cache shouldn't drain the L7 pool — only "fail" does.
	switch parsed.Status {
	case "ok", "pass", "warn":
		// healthy
	default:
		return Status{
			Up:     false,
			Reason: fmt.Sprintf("healthz status=%q", parsed.Status),
		}, nil
	}
	return Status{Up: true}, nil
}

func (h *HTTPController) fetchVersion(ctx context.Context) (string, error) {
	url := h.BaseURL + "/api/v1/version"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("version HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	var v versionResponse
	if err := json.Unmarshal(body, &v); err != nil {
		return "", err
	}
	return v.Version, nil
}
