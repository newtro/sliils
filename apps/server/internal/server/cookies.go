package server

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/config"
)

// RefreshCookieName is the HttpOnly cookie that carries the opaque refresh token.
// Access tokens are returned in the JSON response body and stored in memory
// by the web client — they are NEVER set as cookies (that'd make them usable
// for CSRF and we'd need additional defense).
const RefreshCookieName = "sliils_refresh"

func sameSiteFromConfig(cfg *config.Config) http.SameSite {
	switch cfg.CookieSameSite {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteLaxMode
	}
}

func setRefreshCookie(c echo.Context, cfg *config.Config, refresh string, expires time.Time) {
	c.SetCookie(&http.Cookie{
		Name:     RefreshCookieName,
		Value:    refresh,
		Path:     "/api/v1/auth",
		Domain:   cfg.CookieDomain,
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   cfg.CookieSecure,
		SameSite: sameSiteFromConfig(cfg),
	})
}

func clearRefreshCookie(c echo.Context, cfg *config.Config) {
	c.SetCookie(&http.Cookie{
		Name:     RefreshCookieName,
		Value:    "",
		Path:     "/api/v1/auth",
		Domain:   cfg.CookieDomain,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   cfg.CookieSecure,
		SameSite: sameSiteFromConfig(cfg),
	})
}
