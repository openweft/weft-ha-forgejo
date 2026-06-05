// Package forgejo is the thin shell around the local Forgejo process :
// status probe via /api/healthz, app.ini rendering, on-demand reload
// (`SIGHUP`). The reconcile loop talks to this package rather than
// shelling out directly so unit tests can swap a fake.
package forgejo

import (
	"context"
	"errors"
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
