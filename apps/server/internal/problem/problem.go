// Package problem renders RFC 7807 application/problem+json responses.
package problem

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"github.com/labstack/echo/v4"
)

const contentType = "application/problem+json"

// Details is the RFC 7807 payload. Handlers build it, middleware marshals it.
type Details struct {
	Type     string         `json:"type"`
	Title    string         `json:"title"`
	Status   int            `json:"status"`
	Detail   string         `json:"detail,omitempty"`
	Instance string         `json:"instance,omitempty"`
	Errors   map[string]any `json:"errors,omitempty"`
}

// Error allows Details to satisfy the error interface so handlers can `return`
// a problem and have the central error handler render it.
func (d *Details) Error() string {
	return fmt.Sprintf("%d %s: %s", d.Status, d.Title, d.Detail)
}

// Helpers for the common cases. Keep titles short and stable — docs refer to them.
func New(status int, title, detail string) *Details {
	return &Details{
		Type:   typeFor(status),
		Title:  title,
		Status: status,
		Detail: detail,
	}
}

func BadRequest(detail string) *Details    { return New(http.StatusBadRequest, "Bad request", detail) }
func Unauthorized(detail string) *Details  { return New(http.StatusUnauthorized, "Unauthorized", detail) }
func Forbidden(detail string) *Details     { return New(http.StatusForbidden, "Forbidden", detail) }
func NotFound(detail string) *Details      { return New(http.StatusNotFound, "Not found", detail) }
func Conflict(detail string) *Details      { return New(http.StatusConflict, "Conflict", detail) }
func TooManyRequests(detail string) *Details {
	return New(http.StatusTooManyRequests, "Too many requests", detail)
}
func Internal(detail string) *Details      { return New(http.StatusInternalServerError, "Internal server error", detail) }
func ServiceUnavailable(detail string) *Details {
	return New(http.StatusServiceUnavailable, "Service unavailable", detail)
}

// devMode reports whether the server is running in a development
// context. When true, 5xx responses retain their raw detail string
// for faster local debugging. When false (production), the detail is
// redacted and only the correlation id is returned so DB constraint
// names, decode errors, and path internals never reach the client.
func devMode() bool {
	if v := os.Getenv("SLIILS_DEV_MODE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	// Leaking detail is only safe when we're clearly local — default to
	// "production" (detail-redacted) unless SLIILS_DEV_MODE is set.
	return false
}

// newCorrelationID returns 16 random hex chars — short enough to paste
// in a support ticket, long enough to be unique across log streams.
func newCorrelationID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "correlate-err"
	}
	return hex.EncodeToString(b[:])
}

// ErrorHandler wraps Echo's default handler to render every error as RFC 7807.
// On 5xx responses the detail is redacted and a correlation id is logged
// with the raw cause so operators can tie a user-visible "error reference"
// back to the actual failure without leaking internals to the client.
func ErrorHandler(logger *slog.Logger) echo.HTTPErrorHandler {
	dev := devMode()
	return func(err error, c echo.Context) {
		if c.Response().Committed {
			return
		}

		var d *Details
		if errors.As(err, &d) {
			writeRedacted(c, logger, d, dev)
			return
		}

		var he *echo.HTTPError
		if errors.As(err, &he) {
			dto := &Details{
				Type:   typeFor(he.Code),
				Title:  http.StatusText(he.Code),
				Status: he.Code,
				Detail: fmt.Sprint(he.Message),
			}
			writeRedacted(c, logger, dto, dev)
			return
		}

		corr := newCorrelationID()
		logger.LogAttrs(c.Request().Context(), slog.LevelError, "unhandled error",
			slog.String("error", err.Error()),
			slog.String("path", c.Request().URL.Path),
			slog.String("correlation_id", corr),
		)
		writeJSON(c, &Details{
			Type:   typeFor(http.StatusInternalServerError),
			Title:  "Internal server error",
			Detail: "server error — reference " + corr,
			Status: http.StatusInternalServerError,
		})
	}
}

// writeRedacted strips the detail field for 5xx responses when dev mode
// is off. The raw detail is logged with a fresh correlation id so an
// operator can trace the user-visible reference back to the real cause.
func writeRedacted(c echo.Context, logger *slog.Logger, d *Details, dev bool) {
	if !dev && d.Status >= 500 && d.Detail != "" {
		corr := newCorrelationID()
		logger.LogAttrs(c.Request().Context(), slog.LevelError, "handler error",
			slog.Int("status", d.Status),
			slog.String("title", d.Title),
			slog.String("detail", d.Detail),
			slog.String("path", c.Request().URL.Path),
			slog.String("correlation_id", corr),
		)
		d = &Details{
			Type:   d.Type,
			Title:  d.Title,
			Status: d.Status,
			Detail: "server error — reference " + corr,
		}
	}
	writeJSON(c, d)
}

func writeJSON(c echo.Context, d *Details) {
	c.Response().Header().Set(echo.HeaderContentType, contentType)
	c.Response().WriteHeader(d.Status)
	_ = c.JSON(d.Status, d) // Header is set; JSON just marshals.
}

func typeFor(status int) string {
	return fmt.Sprintf("https://sliils.com/problems/%d", status)
}
