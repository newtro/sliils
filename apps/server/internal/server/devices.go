package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/problem"
)

// Push devices + DND controls (M11).
//
// Surface:
//   GET    /me/devices                 — list my active devices
//   POST   /me/devices                 — register one (idempotent on endpoint)
//   DELETE /me/devices/:id             — remove (explicit "sign this device out")
//   GET    /me/push-public-key         — VAPID public key the client needs
//                                        to subscribe to web push
//   PATCH  /me/dnd                     — update quiet hours / snooze

// ---- DTOs ---------------------------------------------------------------

type DeviceDTO struct {
	ID         int64     `json:"id"`
	Platform   string    `json:"platform"`
	Label      string    `json:"label,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type registerDeviceRequest struct {
	Platform   string `json:"platform"` // web | tauri | apns | fcm | unifiedpush
	Endpoint   string `json:"endpoint"`
	P256DH     string `json:"p256dh,omitempty"`
	AuthSecret string `json:"auth_secret,omitempty"`
	Label      string `json:"label,omitempty"`
}

type patchDNDRequest struct {
	// SnoozeUntil is an RFC3339 instant; null or empty string clears.
	SnoozeUntil *string `json:"snooze_until,omitempty"`
	// QuietHoursStart/End are minutes since midnight. Both nil to clear.
	QuietHoursStart *int   `json:"quiet_hours_start,omitempty"`
	QuietHoursEnd   *int   `json:"quiet_hours_end,omitempty"`
	QuietHoursTZ    string `json:"quiet_hours_tz,omitempty"`
}

// ---- routes -------------------------------------------------------------

func (s *Server) mountDevices(g *echo.Group) {
	g.GET("/devices", s.listDevices)
	g.POST("/devices", s.registerDevice)
	g.DELETE("/devices/:id", s.deleteDevice)
	g.GET("/push-public-key", s.getPushPublicKey)
	g.PATCH("/dnd", s.patchDND)
}

// ---- handlers ----------------------------------------------------------

func (s *Server) getPushPublicKey(c echo.Context) error {
	if s.push == nil {
		return problem.ServiceUnavailable("push is not configured on this server")
	}
	return c.JSON(http.StatusOK, map[string]string{"public_key": s.push.VAPIDPublicKey()})
}

func (s *Server) listDevices(c echo.Context) error {
	user := userFromContext(c)
	if s.ownerPool == nil {
		return problem.Internal("device storage requires the owner pool")
	}
	q := sqlcgen.New(s.ownerPool)
	rows, err := q.ListMyDevices(c.Request().Context(), user.ID)
	if err != nil {
		return problem.Internal("list devices: " + err.Error())
	}
	out := make([]DeviceDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, DeviceDTO{
			ID:         r.ID,
			Platform:   r.Platform,
			Label:      r.Label,
			UserAgent:  r.UserAgent,
			CreatedAt:  r.CreatedAt.Time,
			LastSeenAt: r.LastSeenAt.Time,
		})
	}
	return c.JSON(http.StatusOK, out)
}

func (s *Server) registerDevice(c echo.Context) error {
	user := userFromContext(c)
	if s.ownerPool == nil {
		return problem.Internal("device storage requires the owner pool")
	}

	var req registerDeviceRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	req.Platform = strings.ToLower(strings.TrimSpace(req.Platform))
	req.Endpoint = strings.TrimSpace(req.Endpoint)
	if req.Endpoint == "" {
		return problem.BadRequest("endpoint is required")
	}
	switch req.Platform {
	case "web", "tauri":
		if req.P256DH == "" || req.AuthSecret == "" {
			return problem.BadRequest("web push requires p256dh and auth_secret")
		}
	case "apns", "fcm", "unifiedpush":
		// No key material needed for proxy-dispatched pushes — the
		// endpoint is the device token.
	default:
		return problem.BadRequest("platform must be one of web|tauri|apns|fcm|unifiedpush")
	}

	ua := c.Request().Header.Get("User-Agent")
	q := sqlcgen.New(s.ownerPool)
	row, err := q.RegisterDevice(c.Request().Context(), sqlcgen.RegisterDeviceParams{
		UserID:     user.ID,
		Platform:   req.Platform,
		Endpoint:   req.Endpoint,
		P256dh:     req.P256DH,
		AuthSecret: req.AuthSecret,
		UserAgent:  ua,
		Label:      req.Label,
	})
	if err != nil {
		return problem.Internal("register device: " + err.Error())
	}
	return c.JSON(http.StatusCreated, DeviceDTO{
		ID:         row.ID,
		Platform:   row.Platform,
		Label:      row.Label,
		UserAgent:  row.UserAgent,
		CreatedAt:  row.CreatedAt.Time,
		LastSeenAt: row.LastSeenAt.Time,
	})
}

func (s *Server) deleteDevice(c echo.Context) error {
	user := userFromContext(c)
	if s.ownerPool == nil {
		return problem.Internal("device storage requires the owner pool")
	}
	id, err := parseInt64Param(c, "id")
	if err != nil {
		return err
	}
	q := sqlcgen.New(s.ownerPool)
	if err := q.DeleteMyDevice(c.Request().Context(), sqlcgen.DeleteMyDeviceParams{
		ID:     id,
		UserID: user.ID,
	}); err != nil {
		return problem.Internal("delete device: " + err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) patchDND(c echo.Context) error {
	user := userFromContext(c)
	if s.ownerPool == nil {
		return problem.Internal("dnd requires the owner pool")
	}
	var req patchDNDRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}

	var snoozeUntil pgtype.Timestamptz
	if req.SnoozeUntil != nil && *req.SnoozeUntil != "" {
		t, err := time.Parse(time.RFC3339, *req.SnoozeUntil)
		if err != nil {
			return problem.BadRequest("snooze_until must be RFC3339: " + err.Error())
		}
		snoozeUntil = pgtype.Timestamptz{Time: t, Valid: true}
	}

	// Persist quiet-hours minutes as int32 to match the sqlc param shape.
	var qhStart *int32
	var qhEnd *int32
	if req.QuietHoursStart != nil {
		if *req.QuietHoursStart < 0 || *req.QuietHoursStart > 1440 {
			return problem.BadRequest("quiet_hours_start must be 0..1440")
		}
		v := int32(*req.QuietHoursStart)
		qhStart = &v
	}
	if req.QuietHoursEnd != nil {
		if *req.QuietHoursEnd < 0 || *req.QuietHoursEnd > 1440 {
			return problem.BadRequest("quiet_hours_end must be 0..1440")
		}
		v := int32(*req.QuietHoursEnd)
		qhEnd = &v
	}
	if (qhStart == nil) != (qhEnd == nil) {
		return problem.BadRequest("quiet_hours_start and quiet_hours_end must be set together")
	}
	var tz *string
	if req.QuietHoursTZ != "" {
		// Validate the IANA name up-front so a bad tz surfaces here
		// rather than silently falling back at delivery time.
		if _, err := time.LoadLocation(req.QuietHoursTZ); err != nil {
			return problem.BadRequest("unknown quiet_hours_tz: " + err.Error())
		}
		t := req.QuietHoursTZ
		tz = &t
	}

	q := sqlcgen.New(s.ownerPool)
	if err := q.UpdateUserQuietHours(c.Request().Context(), sqlcgen.UpdateUserQuietHoursParams{
		ID:                user.ID,
		DndEnabledUntil:   snoozeUntil,
		QuietHoursStart:   qhStart,
		QuietHoursEnd:     qhEnd,
		QuietHoursTz:      tz,
	}); err != nil {
		return problem.Internal("update dnd: " + err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}
