package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sliils/sliils/apps/server/internal/health"
)

func TestHealthHandlerAlwaysOK(t *testing.T) {
	e := echo.New()
	e.GET("/healthz", health.Handler())
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestReadyHandlerAllChecksPass(t *testing.T) {
	r := health.NewRegistry()
	r.Register("db", func(_ context.Context) error { return nil })
	r.Register("cache", func(_ context.Context) error { return nil })

	e := echo.New()
	e.GET("/readyz", r.ReadyHandler())
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "ready", body["status"])
}

func TestReadyHandlerFailsWhenAnyCheckFails(t *testing.T) {
	r := health.NewRegistry()
	r.Register("db", func(_ context.Context) error { return nil })
	r.Register("cache", func(_ context.Context) error { return errors.New("connection refused") })

	e := echo.New()
	e.GET("/readyz", r.ReadyHandler())
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "not_ready", body["status"])

	checks, ok := body["checks"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "ok", checks["db"])
	assert.Equal(t, "connection refused", checks["cache"])
}
