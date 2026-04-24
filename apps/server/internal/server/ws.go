package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/problem"
	"github.com/sliils/sliils/apps/server/internal/realtime"
)

// ---- WebSocket protocol (tech-spec §2.3) --------------------------------
//
// The client / server exchange is framed as JSON envelopes:
//
//     {"v": 1, "type": "...", "id": "optional-client-msg-id", "data": {...}}
//
// Server → client types currently implemented:
//
//   hello               initial handshake; includes last_event_id and
//                       ping_interval so the client can decide whether to
//                       subscribe with a since= replay.
//   event               carries a realtime.Event — type lives inside data.
//   error               protocol error from the client; description in data.
//   must_resync         server couldn't replay since the provided event id;
//                       client should drop local state and refetch.
//
// Client → server types currently implemented:
//
//   subscribe           data.topics = ["ws:1:ch:2", ...]; optional data.since
//                       triggers replay before subscribing to new events.
//   unsubscribe         data.topics = [...]
//   ping                server replies with type="pong"

const (
	wsPingInterval      = 30 * time.Second
	wsWriteTimeout      = 10 * time.Second
	wsReadTimeout       = 60 * time.Second
	wsMaxMessageBytes   = 64 * 1024
	wsOutboundBuffer    = 64
)

type wsEnvelope struct {
	V    int             `json:"v"`
	Type string          `json:"type"`
	ID   string          `json:"id,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

type wsEventPayload struct {
	ID        int64           `json:"id"`
	Type      string          `json:"type"`
	Topic     string          `json:"topic"`
	Data      json.RawMessage `json:"data"`
	Timestamp time.Time       `json:"ts"`
}

type wsSubscribePayload struct {
	Topics []string `json:"topics"`
	Since  int64    `json:"since,omitempty"`
}

type wsHelloPayload struct {
	LastEventID  int64         `json:"last_event_id"`
	PingInterval time.Duration `json:"ping_interval_ms"`
	ServerTime   time.Time     `json:"server_time"`
}

// upgrader has its CheckOrigin disabled at the library level because we
// need access to *Server.cfg (for PublicBaseURL + AllowDevOrigins) to
// decide which origins to accept. The actual origin check happens in
// handleSocket before upgrader.Upgrade is called.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  4 * 1024,
	WriteBufferSize: 4 * 1024,
	CheckOrigin: func(r *http.Request) bool {
		// Per-request check lives in handleSocket; this closure always
		// returns true so the upgrade proceeds after we've already
		// validated.
		return true
	},
}

// mountWS attaches the /socket endpoint. Authentication is via ?token=<jwt>
// on the upgrade request because most browsers can't set Authorization
// headers during a WebSocket handshake.
func (s *Server) mountWS(api *echo.Group) {
	api.GET("/socket", s.handleSocket)
}

// isAllowedWSOrigin returns true if the given Origin header is safe:
//   - empty origin (native app, curl, desktop Tauri shell) — accepted
//     because those clients don't set one and their traffic is already
//     gated by the access token.
//   - same-origin (Origin host matches Request host) — accepted.
//   - explicit match against PublicBaseURL / allowed dev origins.
//
// Anything else (foreign web page, bolted-on browser extension) is
// refused so a stolen access token from user state cannot be exploited
// cross-origin.
func (s *Server) isAllowedWSOrigin(origin, host string) bool {
	if origin == "" {
		return true
	}
	if u, err := url.Parse(origin); err == nil && u.Host != "" && strings.EqualFold(u.Host, host) {
		return true
	}
	for _, allowed := range allowedOrigins(s.cfg) {
		if origin == allowed {
			return true
		}
	}
	return false
}

func (s *Server) handleSocket(c echo.Context) error {
	// Origin enforcement: reject upgrade if the Origin header is cross-
	// origin AND doesn't match PublicBaseURL or an allowed dev origin.
	// Without this, any web page could open a WebSocket against the API
	// using a stolen access token from the user's local state.
	if !s.isAllowedWSOrigin(c.Request().Header.Get("Origin"), c.Request().Host) {
		return problem.Forbidden("origin not allowed")
	}

	tokenStr := c.QueryParam("token")
	if tokenStr == "" {
		return problem.Unauthorized("missing ?token=<jwt>")
	}
	claims, err := s.tokens.Parse(tokenStr)
	if err != nil {
		return problem.Unauthorized("invalid access token")
	}

	user, err := s.queries.GetUserByID(c.Request().Context(), claims.UserID)
	if err != nil {
		return problem.Unauthorized("user not found")
	}

	ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return fmt.Errorf("ws upgrade: %w", err)
	}

	ws.SetReadLimit(wsMaxMessageBytes)
	conn := &wsConn{
		srv:         s,
		userID:      user.ID,
		ws:          ws,
		out:         make(chan wsEnvelope, wsOutboundBuffer),
		done:        make(chan struct{}),
		logger:      s.logger.With(slog.Int64("ws_user_id", user.ID)),
		topics:      make(map[string]struct{}),
		presenceSet: make(map[int64]struct{}),
	}
	conn.run()
	return nil
}

// wsConn bundles one authenticated WebSocket connection.
//
// Lifecycle invariants that matter for thread-safety:
//   - `out` has multiple senders (pump, reader dispatch, handshake) and a
//     single consumer (writer goroutine). We never close it — the writer
//     exits on `done` and the channel is GC'd with the connection.
//   - `done` is closed exactly once, inside cleanup under closedMu. Every
//     sender selects on `<-done` first so a send can't race a cleanup.
//   - `closed` guards cleanup against double-invocation (reader + writer
//     both finish and both defer cleanup).
type wsConn struct {
	srv         *Server
	userID      int64
	ws          *websocket.Conn
	out         chan wsEnvelope
	done        chan struct{}
	sub         *realtime.Subscriber
	unsub       func()
	subMu       sync.Mutex
	topics      map[string]struct{} // topics this conn is currently subscribed to
	presenceSet map[int64]struct{}  // workspace ids this conn has entered for presence
	logger      *slog.Logger
	closed      bool
	closedMu    sync.Mutex
}

func (c *wsConn) run() {
	defer c.cleanup()

	// Handshake: send hello so the client knows the server's current event
	// ID and whether to send `subscribe` with a `since` field.
	hello := wsHelloPayload{
		LastEventID:  c.srv.broker.LastEventID(),
		PingInterval: wsPingInterval,
		ServerTime:   time.Now().UTC(),
	}
	if err := c.send(wsEnvelope{V: 1, Type: "hello", Data: mustJSON(hello)}); err != nil {
		c.logger.Warn("ws hello failed", slog.String("error", err.Error()))
		return
	}

	writerDone := make(chan struct{})
	go c.writer(writerDone)
	c.reader()
	<-writerDone
}

func (c *wsConn) reader() {
	c.ws.SetReadDeadline(time.Now().Add(wsReadTimeout))
	c.ws.SetPongHandler(func(string) error {
		c.ws.SetReadDeadline(time.Now().Add(wsReadTimeout))
		return nil
	})

	for {
		_, raw, err := c.ws.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return
			}
			if !strings.Contains(err.Error(), "use of closed network connection") {
				c.logger.Debug("ws read ended", slog.String("error", err.Error()))
			}
			return
		}

		var env wsEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			c.sendErr("invalid envelope")
			continue
		}
		c.dispatch(env)
	}
}

func (c *wsConn) dispatch(env wsEnvelope) {
	switch env.Type {
	case "subscribe":
		var p wsSubscribePayload
		if err := json.Unmarshal(env.Data, &p); err != nil {
			c.sendErr("invalid subscribe payload")
			return
		}
		c.handleSubscribe(p)

	case "unsubscribe":
		var p wsSubscribePayload
		if err := json.Unmarshal(env.Data, &p); err != nil {
			c.sendErr("invalid unsubscribe payload")
			return
		}
		c.subMu.Lock()
		if c.sub != nil {
			c.srv.broker.RemoveTopics(c.sub, p.Topics)
		}
		for _, t := range p.Topics {
			delete(c.topics, t)
		}
		c.subMu.Unlock()

	case "ping":
		_ = c.send(wsEnvelope{V: 1, Type: "pong", ID: env.ID})

	case "typing.heartbeat":
		var p struct {
			WorkspaceID int64 `json:"workspace_id"`
			ChannelID   int64 `json:"channel_id"`
		}
		if err := json.Unmarshal(env.Data, &p); err != nil {
			c.sendErr("invalid typing payload")
			return
		}
		// Only relay typing heartbeats for topics the user actually joined.
		if !c.hasTopic(realtime.TopicChannel(p.WorkspaceID, p.ChannelID)) {
			return
		}
		c.srv.typing.Heartbeat(p.WorkspaceID, p.ChannelID, c.userID)

	case "typing.stopped":
		var p struct {
			WorkspaceID int64 `json:"workspace_id"`
			ChannelID   int64 `json:"channel_id"`
		}
		if err := json.Unmarshal(env.Data, &p); err != nil {
			c.sendErr("invalid typing payload")
			return
		}
		c.srv.typing.Stop(p.WorkspaceID, p.ChannelID, c.userID)

	default:
		c.sendErr("unknown type: " + env.Type)
	}
}

// hasTopic reports whether this connection has subscribed to the topic.
// Used to gate typing heartbeats so a client can't spam typing events
// for channels it isn't in.
func (c *wsConn) hasTopic(topic string) bool {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	_, ok := c.topics[topic]
	return ok
}

// handleSubscribe validates every requested topic against the user's actual
// access (via RLS-backed DB lookup) before attaching the subscriber. This
// is the choke-point that prevents a crafted `subscribe` payload from
// letting someone listen in on another workspace's channels.
func (c *wsConn) handleSubscribe(p wsSubscribePayload) {
	allowed, err := c.authorizeTopics(p.Topics)
	if err != nil {
		c.sendErr("authorize: " + err.Error())
		return
	}
	if len(allowed) == 0 {
		c.sendErr("no authorized topics in subscribe")
		return
	}

	// Replay first so the client sees any missed events in order before new
	// ones start streaming.
	if p.Since > 0 {
		events, complete := c.srv.broker.Replay(allowed, p.Since)
		if !complete {
			_ = c.send(wsEnvelope{V: 1, Type: "must_resync"})
		}
		for _, ev := range events {
			_ = c.sendEvent(ev)
		}
	}

	c.subMu.Lock()
	if c.sub == nil {
		sub, unsub := c.srv.broker.Subscribe(allowed)
		c.sub = sub
		c.unsub = unsub
		go c.pump()
	} else {
		c.srv.broker.AddTopics(c.sub, allowed)
	}
	for _, t := range allowed {
		c.topics[t] = struct{}{}
	}
	c.subMu.Unlock()

	// Workspace-scoped topic subscription tells us which workspace(s) the
	// user is actively watching, so presence tracks them as online there.
	for _, t := range allowed {
		if wsID, ok := parseWorkspaceTopic(t); ok {
			if _, already := c.presenceSet[wsID]; !already {
				c.presenceSet[wsID] = struct{}{}
				c.srv.presence.Enter(wsID, c.userID)
				// Send the initial snapshot so the client paints existing
				// online-dots without waiting for the next transition.
				snapshot := c.srv.presence.Snapshot(wsID)
				_ = c.send(wsEnvelope{V: 1, Type: "presence.snapshot", Data: mustJSON(map[string]any{
					"workspace_id": wsID,
					"user_ids":     snapshot,
				})})
			}
		}
	}
}

// authorizeTopics keeps only the topics this user is allowed to listen to.
// Each topic is parsed and re-validated against the DB under the user's
// GUC so RLS policies decide visibility.
func (c *wsConn) authorizeTopics(topics []string) ([]string, error) {
	out := make([]string, 0, len(topics))
	for _, t := range topics {
		// Workspace-level topic: authorize via workspace membership lookup.
		if wsID, ok := parseWorkspaceTopic(t); ok {
			ok, err := c.userIsWorkspaceMember(wsID)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, t)
			}
			continue
		}

		// Channel-level topic.
		ws, ch, ok := parseChannelTopic(t)
		if !ok {
			continue
		}
		ok, err := c.userCanReadChannel(ws, ch)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// userIsWorkspaceMember confirms the user has an active membership in the
// given workspace via the workspaces RLS policy.
func (c *wsConn) userIsWorkspaceMember(workspaceID int64) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	allowed := false
	err := db.WithTx(ctx, c.srv.pool.Pool, db.TxOptions{UserID: c.userID, ReadOnly: true}, func(scope db.TxScope) error {
		_, err := scope.Queries.GetWorkspaceByID(ctx, workspaceID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		allowed = true
		return nil
	})
	return allowed, err
}

// userCanReadChannel is the RLS-backed check for whether this user may
// observe events in a channel. Non-member → false.
func (c *wsConn) userCanReadChannel(workspaceID, channelID int64) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	allowed := false
	err := db.WithTx(ctx, c.srv.pool.Pool, db.TxOptions{UserID: c.userID, WorkspaceID: workspaceID, ReadOnly: true}, func(scope db.TxScope) error {
		_, err := scope.Queries.GetWorkspaceByID(ctx, workspaceID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil // not a member; allowed stays false
			}
			return err
		}
		ch, err := scope.Queries.GetChannelByID(ctx, channelID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		if ch.WorkspaceID != workspaceID {
			return nil
		}
		// M3: all public channels visible to every workspace member. Private
		// channel logic lands in M4 when channel_memberships gates it.
		allowed = ch.Type == "public"
		return nil
	})
	return allowed, err
}

// pump forwards events from the broker subscription out to the socket.
func (c *wsConn) pump() {
	for ev := range c.sub.C() {
		if err := c.sendEvent(ev); err != nil {
			return
		}
	}
}

func (c *wsConn) writer(writerDone chan struct{}) {
	defer close(writerDone)
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			// Drain any buffered envelopes on a best-effort basis before
			// sending the close frame. Non-blocking so a stuck peer doesn't
			// prevent shutdown.
			for {
				select {
				case env := <-c.out:
					c.ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
					if err := c.ws.WriteJSON(env); err != nil {
						break
					}
				default:
					goto closeFrame
				}
			}
		closeFrame:
			_ = c.ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return
		case env := <-c.out:
			c.ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if err := c.ws.WriteJSON(env); err != nil {
				return
			}
		case <-ticker.C:
			c.ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// send queues an envelope on the outbound channel. Selecting on c.done
// first is what prevents the "send on closed channel" race: once cleanup
// has closed `done`, every in-flight send returns immediately with an
// error instead of blocking on a channel that's about to be GC'd.
func (c *wsConn) send(env wsEnvelope) error {
	select {
	case <-c.done:
		return errors.New("connection closed")
	default:
	}
	select {
	case <-c.done:
		return errors.New("connection closed")
	case c.out <- env:
		return nil
	case <-time.After(wsWriteTimeout):
		return errors.New("outbound buffer full")
	}
}

func (c *wsConn) sendEvent(ev realtime.Event) error {
	payload := wsEventPayload{
		ID:        ev.ID,
		Type:      ev.Type,
		Topic:     ev.Topic,
		Data:      ev.Data,
		Timestamp: ev.Timestamp,
	}
	return c.send(wsEnvelope{V: 1, Type: "event", Data: mustJSON(payload)})
}

func (c *wsConn) sendErr(msg string) {
	_ = c.send(wsEnvelope{V: 1, Type: "error", Data: mustJSON(map[string]string{"message": msg})})
}

func (c *wsConn) cleanup() {
	c.closedMu.Lock()
	if c.closed {
		c.closedMu.Unlock()
		return
	}
	c.closed = true
	// Closing `done` signals every sender (pump, dispatch) to bail out
	// before touching `out`. MUST happen under closedMu so a concurrent
	// cleanup can't double-close.
	close(c.done)
	c.closedMu.Unlock()

	// Tell presence each workspace we were watching that we're gone.
	for wsID := range c.presenceSet {
		c.srv.presence.Leave(wsID, c.userID)
	}

	if c.unsub != nil {
		c.unsub()
	}
	// Intentionally NOT closing c.out here. Senders are gated on <-c.done;
	// the writer goroutine drains + exits on the same signal. Closing a
	// shared-sender channel from the consumer is the canonical way to
	// trigger a "send on closed channel" panic.
	_ = c.ws.Close()
}

// ---- helpers -------------------------------------------------------------

// parseChannelTopic splits "ws:{wsID}:ch:{chID}" into its ids.
func parseChannelTopic(topic string) (workspaceID, channelID int64, ok bool) {
	parts := strings.Split(topic, ":")
	if len(parts) != 4 || parts[0] != "ws" || parts[2] != "ch" {
		return 0, 0, false
	}
	ws, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	ch, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return ws, ch, true
}

// parseWorkspaceTopic splits "ws:{wsID}" (no channel suffix) into its id.
func parseWorkspaceTopic(topic string) (workspaceID int64, ok bool) {
	parts := strings.Split(topic, ":")
	if len(parts) != 2 || parts[0] != "ws" {
		return 0, false
	}
	ws, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return ws, true
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

