// Package problem renders RFC 7807 application/problem+json responses.
package problem

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

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

// ErrorHandler wraps Echo's default handler to render every error as RFC 7807.
func ErrorHandler(logger *slog.Logger) echo.HTTPErrorHandler {
	return func(err error, c echo.Context) {
		if c.Response().Committed {
			return
		}

		var d *Details
		if errors.As(err, &d) {
			writeJSON(c, d)
			return
		}

		var he *echo.HTTPError
		if errors.As(err, &he) {
			writeJSON(c, &Details{
				Type:   typeFor(he.Code),
				Title:  http.StatusText(he.Code),
				Status: he.Code,
				Detail: fmt.Sprint(he.Message),
			})
			return
		}

		logger.LogAttrs(c.Request().Context(), slog.LevelError, "unhandled error",
			slog.String("error", err.Error()),
			slog.String("path", c.Request().URL.Path),
		)
		writeJSON(c, &Details{
			Type:   typeFor(http.StatusInternalServerError),
			Title:  "Internal server error",
			Status: http.StatusInternalServerError,
		})
	}
}

func writeJSON(c echo.Context, d *Details) {
	c.Response().Header().Set(echo.HeaderContentType, contentType)
	c.Response().WriteHeader(d.Status)
	_ = c.JSON(d.Status, d) // Header is set; JSON just marshals.
}

func typeFor(status int) string {
	return fmt.Sprintf("https://sliils.com/problems/%d", status)
}
