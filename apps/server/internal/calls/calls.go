// Package calls owns the LiveKit integration for SliilS (M8).
//
// Two responsibilities:
//
//   - JWT issuance. When a user asks to join a meeting, we mint a short-
//     lived LiveKit access token that grants them the ability to join the
//     specific room + publish/subscribe tracks. LiveKit validates the
//     HMAC itself, so a compromised client can't upgrade its own grants.
//
//   - Room administration. Optional — for features like end-the-call-
//     for-everyone, participant removal, and egress (recording) control.
//     We wire the RoomService client but expose a small facade so the
//     rest of the codebase stays blissfully unaware of LiveKit's API.
package calls

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	lkauth "github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// Client bundles the config every caller needs: the API key/secret pair
// for signing tokens, the WS URL to hand to browser clients, and a
// lazily-built RoomService client.
type Client struct {
	apiKey    string
	apiSecret string
	httpURL   string
	wsURL     string
	logger    *slog.Logger

	roomSvc *lksdk.RoomServiceClient // lazy init; nil until first admin call
}

type Options struct {
	APIKey    string
	APISecret string
	HTTPURL   string // server-side URL (used for RoomService RPCs)
	WSURL     string // client-facing URL (goes into join token responses)
	Logger    *slog.Logger
}

func NewClient(opts Options) (*Client, error) {
	if opts.APIKey == "" || opts.APISecret == "" {
		return nil, errors.New("livekit api key + secret are required")
	}
	if opts.HTTPURL == "" {
		return nil, errors.New("livekit http url is required")
	}
	if opts.WSURL == "" {
		opts.WSURL = opts.HTTPURL
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Client{
		apiKey:    opts.APIKey,
		apiSecret: opts.APISecret,
		httpURL:   opts.HTTPURL,
		wsURL:     opts.WSURL,
		logger:    opts.Logger,
	}, nil
}

// WSURL is the server URL clients connect to with their join token.
func (c *Client) WSURL() string { return c.wsURL }

// ---- Token issuance ----------------------------------------------------

// JoinClaims captures the inputs for a single participant token. All the
// caller-friendly fields go here; the RoomName is canonicalized by the
// server (we never let a client ask to join "any" room).
type JoinClaims struct {
	RoomName        string
	ParticipantID   string // unique per user within the room (we use user.ID stringified)
	DisplayName     string // shown in the call UI
	CanPublish      bool
	CanSubscribe    bool
	CanPublishData  bool
	CanUpdateRoom   bool          // for the host; lets them kick + end
	TTL             time.Duration // how long the token is valid
}

// IssueJoin mints a LiveKit access token from the given claims. Safe to
// call concurrently; AccessToken is a value type and each call HMACs its
// own fresh payload.
func (c *Client) IssueJoin(claims JoinClaims) (string, error) {
	if claims.RoomName == "" {
		return "", errors.New("room name is required")
	}
	if claims.ParticipantID == "" {
		return "", errors.New("participant id is required")
	}
	if claims.TTL <= 0 {
		claims.TTL = 2 * time.Hour
	}
	at := lkauth.NewAccessToken(c.apiKey, c.apiSecret).
		SetIdentity(claims.ParticipantID).
		SetName(claims.DisplayName).
		SetValidFor(claims.TTL).
		SetVideoGrant(&lkauth.VideoGrant{
			RoomJoin:     true,
			Room:         claims.RoomName,
			CanPublish:   ptrBool(claims.CanPublish),
			CanSubscribe: ptrBool(claims.CanSubscribe),
			CanPublishData: ptrBool(claims.CanPublishData),
			RoomAdmin:    claims.CanUpdateRoom,
		})
	tok, err := at.ToJWT()
	if err != nil {
		return "", fmt.Errorf("sign join token: %w", err)
	}
	return tok, nil
}

// ---- Room administration ----------------------------------------------

// EndRoom closes a LiveKit room for every participant. Called by the host
// when they click "End for everyone" or by the server when the last
// invited participant leaves a 1:1 DM call.
func (c *Client) EndRoom(ctx context.Context, roomName string) error {
	svc := c.roomService()
	_, err := svc.DeleteRoom(ctx, &livekit.DeleteRoomRequest{Room: roomName})
	if err != nil {
		return fmt.Errorf("delete room: %w", err)
	}
	return nil
}

// ListParticipants returns the current attendee roster. Useful for the
// server to confirm a room is empty before cleaning up.
func (c *Client) ListParticipants(ctx context.Context, roomName string) ([]*livekit.ParticipantInfo, error) {
	svc := c.roomService()
	resp, err := svc.ListParticipants(ctx, &livekit.ListParticipantsRequest{Room: roomName})
	if err != nil {
		return nil, err
	}
	return resp.Participants, nil
}

// Health is a liveness probe wired from /readyz. We don't have a
// dedicated health endpoint on LiveKit's side so we do a cheap
// ListRooms; a slow/unreachable LiveKit surfaces as a /readyz failure.
func (c *Client) Health(ctx context.Context) error {
	svc := c.roomService()
	// Use a short timeout on the RPC so readyz doesn't stall.
	hctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := svc.ListRooms(hctx, &livekit.ListRoomsRequest{})
	return err
}

// ---- internal ----------------------------------------------------------

func (c *Client) roomService() *lksdk.RoomServiceClient {
	if c.roomSvc == nil {
		c.roomSvc = lksdk.NewRoomServiceClient(c.httpURL, c.apiKey, c.apiSecret)
	}
	return c.roomSvc
}

// RoomNameForMeeting is the canonical LiveKit room name for a SliilS
// meeting id. One place so clients can never spoof a different meeting's
// room by hand-crafting the name.
func RoomNameForMeeting(meetingID int64) string {
	return "sliils-meeting-" + strconv.FormatInt(meetingID, 10)
}

func ptrBool(b bool) *bool { return &b }
