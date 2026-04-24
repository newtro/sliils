// Package health provides liveness and readiness probes.
//
// /healthz returns 200 as long as the process is running (used by Docker / k8s liveness).
// /readyz runs registered readiness checks; returns 200 only when every check passes.
// Individual checks must be fast (<1s) and non-blocking. Long-running checks belong in
// a background loop that updates a cached boolean.
package health

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
)

// Check returns nil when the dependency is healthy.
type Check func(ctx context.Context) error

// Registry aggregates named readiness checks.
type Registry struct {
	mu     sync.RWMutex
	checks map[string]Check
}

func NewRegistry() *Registry {
	return &Registry{checks: make(map[string]Check)}
}

// Register adds or replaces a named readiness check.
func (r *Registry) Register(name string, check Check) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[name] = check
}

// Handler returns the /healthz handler (always 200 while process is up).
func Handler() echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{
			"status":  "ok",
			"service": "sliils-app",
		})
	}
}

// ReadyHandler runs every registered check and reports aggregate readiness.
func (r *Registry) ReadyHandler() echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
		defer cancel()

		r.mu.RLock()
		checks := make(map[string]Check, len(r.checks))
		for name, check := range r.checks {
			checks[name] = check
		}
		r.mu.RUnlock()

		results := make(map[string]string, len(checks))
		allOK := true
		for name, check := range checks {
			if err := check(ctx); err != nil {
				results[name] = err.Error()
				allOK = false
			} else {
				results[name] = "ok"
			}
		}

		status := http.StatusOK
		outcome := "ready"
		if !allOK {
			status = http.StatusServiceUnavailable
			outcome = "not_ready"
		}

		return c.JSON(status, map[string]any{
			"status": outcome,
			"checks": results,
		})
	}
}
