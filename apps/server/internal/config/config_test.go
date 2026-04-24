package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sliils/sliils/apps/server/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("SLIILS_LISTEN_ADDR", "")
	t.Setenv("SLIILS_LOG_LEVEL", "")
	t.Setenv("SLIILS_LOG_FORMAT", "")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, ":8080", cfg.ListenAddr)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, "json", cfg.LogFormat)
	assert.Equal(t, 15*time.Second, cfg.ReadTimeout)
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("SLIILS_LISTEN_ADDR", ":9090")
	t.Setenv("SLIILS_LOG_LEVEL", "debug")
	t.Setenv("SLIILS_LOG_FORMAT", "text")
	t.Setenv("SLIILS_READ_TIMEOUT", "30s")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, ":9090", cfg.ListenAddr)
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.Equal(t, "text", cfg.LogFormat)
	assert.Equal(t, 30*time.Second, cfg.ReadTimeout)
}

func TestLoadRejectsInvalidLogFormat(t *testing.T) {
	t.Setenv("SLIILS_LOG_FORMAT", "yaml")
	_, err := config.Load()
	assert.Error(t, err)
}

func TestBadDurationFallsBackToDefault(t *testing.T) {
	t.Setenv("SLIILS_READ_TIMEOUT", "not-a-duration")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, 15*time.Second, cfg.ReadTimeout)
}
