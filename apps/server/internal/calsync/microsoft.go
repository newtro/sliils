package calsync

import (
	"context"
	"errors"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/microsoft"
)

// Microsoft provider. OAuth is wired (the flow will succeed), but the
// actual Pull/Push/Delete against Microsoft Graph is scaffolded for
// M9.1 — the acceptance gate only calls out the Google round-trip.
// Shipping the OAuth surface now means users can "connect their
// Outlook" UI-wise; the sync workers no-op until full implementation.
type Microsoft struct {
	oauth *oauth2.Config
}

func NewMicrosoft(clientID, clientSecret, redirectURL string) *Microsoft {
	return &Microsoft{
		oauth: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Endpoint:     microsoft.AzureADEndpoint("common"),
			Scopes: []string{
				"offline_access",
				"User.Read",
				"Calendars.ReadWrite",
			},
		},
	}
}

func (m *Microsoft) Name() string { return "microsoft" }

func (m *Microsoft) AuthCodeURL(state string) string {
	return m.oauth.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

func (m *Microsoft) Exchange(ctx context.Context, code string) (*oauth2.Token, error) {
	return m.oauth.Exchange(ctx, code)
}

// The remaining methods return a sentinel error until M9.1 lands the
// Microsoft Graph SDK calls. The sync workers treat this as "connection
// exists but can't sync yet"; the UI surfaces this state plainly.

var errMicrosoftNotImplemented = errors.New("microsoft calendar sync not yet implemented (M9.1)")

func (m *Microsoft) AccountEmail(ctx context.Context, refreshToken string) (string, error) {
	return "", errMicrosoftNotImplemented
}

func (m *Microsoft) Pull(ctx context.Context, refreshToken, syncToken string) (*PullResult, error) {
	return nil, errMicrosoftNotImplemented
}

func (m *Microsoft) Push(ctx context.Context, refreshToken string, evt *Event, existingID string) (string, string, error) {
	return "", "", errMicrosoftNotImplemented
}

func (m *Microsoft) Delete(ctx context.Context, refreshToken, externalID string) error {
	return errMicrosoftNotImplemented
}

// IsUnimplemented lets callers differentiate "Microsoft hasn't shipped
// yet" from actual API errors, so the pull worker can skip without
// noise in the logs.
func IsUnimplemented(err error) bool {
	return errors.Is(err, errMicrosoftNotImplemented)
}
