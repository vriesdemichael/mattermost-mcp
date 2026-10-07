// Package mattermost is mm-mcp's access to the Mattermost REST API.
//
// It wraps Mattermost's own Go client, model.Client4, from the server's public
// module at the commit of the newest supported release (ADR-024). The client
// and the model types are the server's own, exercised by Mattermost's API test
// suite, so what a request sends and what an answer holds are not guessed here.
// What this package adds is mm-mcp's transport, one error type, and, where a
// supported release differs, the handling of that difference (ADR-025).
package mattermost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/vriesdemichael/mattermost-mcp/internal/version"
)

// RequestTimeout bounds one request to Mattermost.
const RequestTimeout = 30 * time.Second

// Client is one Mattermost identity on one server.
//
// Personal access tokens, bot tokens and session tokens are all sent the same
// way, as a bearer token; they differ in how they are obtained and how long
// they live (ADR-019), which is not this type's concern.
type Client struct {
	api *model.Client4
}

// New builds a client for the server at address, acting with token, over transport.
func New(address, token string, transport http.RoundTripper) *Client {
	api := model.NewAPIv4Client(address)
	api.HTTPClient = &http.Client{Transport: transport, Timeout: RequestTimeout}
	api.HTTPHeader["User-Agent"] = "mm-mcp/" + version.Version
	api.SetToken(token)
	return &Client{api: api}
}

// Error is an answer Mattermost gave with an error status.
type Error struct {
	Status    int
	ID        string
	Message   string
	RequestID string
}

func (e *Error) Error() string {
	id := e.ID
	if id == "" {
		id = "no error id"
	}
	return fmt.Sprintf("Mattermost answered %d (%s): %s", e.Status, id, e.Message)
}

// Me is the user the credential belongs to.
func (c *Client) Me(ctx context.Context) (*model.User, error) {
	user, response, err := c.api.GetMe(ctx, "")
	if err != nil {
		return nil, translate(response, err)
	}
	return user, nil
}

// translate turns what Client4 returns on failure into an *Error when
// Mattermost answered, and leaves a failure to reach it as it is.
func translate(response *model.Response, err error) error {
	var appErr *model.AppError
	if errors.As(err, &appErr) {
		status := appErr.StatusCode
		if status == 0 && response != nil {
			status = response.StatusCode
		}
		return &Error{Status: status, ID: appErr.Id, Message: appErr.Message, RequestID: appErr.RequestId}
	}
	if response != nil && response.StatusCode >= http.StatusBadRequest {
		return &Error{Status: response.StatusCode, Message: err.Error(), RequestID: response.RequestId}
	}
	return fmt.Errorf("could not reach Mattermost: %w", err)
}
