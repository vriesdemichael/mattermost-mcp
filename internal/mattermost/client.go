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
	"strconv"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/vriesdemichael/mm-mcp/internal/network"
	"github.com/vriesdemichael/mm-mcp/internal/version"
)

// RequestTimeout bounds one request to Mattermost that carries no file.
const RequestTimeout = 30 * time.Second

// TransferTimeout bounds one request that uploads or downloads a file. Go's
// client timeout covers reading the whole body, so a 100 MiB file on a slow
// line would fail within RequestTimeout.
const TransferTimeout = 15 * time.Minute

// Client is one Mattermost identity on one server.
//
// Personal access tokens, bot tokens and session tokens are all sent the same
// way, as a bearer token; they differ in how they are obtained and how long
// they live (ADR-019), which is not this type's concern.
type Client struct {
	api *model.Client4
	// transfer is api with TransferTimeout, for the requests that carry a file.
	transfer *model.Client4
}

// New builds a client for the server at address, acting with token, over transport.
func New(address, token string, transport http.RoundTripper) *Client {
	return &Client{
		api:      client4(address, token, transport, RequestTimeout),
		transfer: client4(address, token, transport, TransferTimeout),
	}
}

func client4(address, token string, transport http.RoundTripper, timeout time.Duration) *model.Client4 {
	api := model.NewAPIv4Client(address)
	api.HTTPClient = &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: network.SameOriginRedirects}
	api.HTTPHeader["User-Agent"] = "mm-mcp/" + version.Version
	api.SetToken(token)
	return api
}

// Address is the address the server is served at, as MM_URL gives it.
func (c *Client) Address() string { return c.api.URL }

// Permalink is the address a person opens a post at, on any team.
func (c *Client) Permalink(postID string) string {
	if postID == "" {
		return ""
	}
	return strings.TrimRight(c.api.URL, "/") + "/_redirect/pl/" + postID
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
	message := fmt.Sprintf("Mattermost answered %d (%s): %s", e.Status, id, e.Message)
	if e.Status == http.StatusUnauthorized {
		message += " " + RefusedCredential
	}
	return message
}

// RefusedCredential is what a 401 means for mm-mcp: the credential it was
// started with no longer works, whatever Mattermost's own words suggest.
const RefusedCredential = "The credential mm-mcp was started with was refused: the token in MM_TOKEN is wrong, was revoked, or is a session that expired. " +
	"Get a new one, with `mm-mcp login` or as a personal access token, and restart the server."

// OldestSupported is the oldest Mattermost release mm-mcp supports, the
// Extended Support Release its live suite runs against (ADR-025). A
// governance test holds it to docker/esr.
const OldestSupported = "11.7"

// Check is who the credential belongs to, and which release the server runs,
// as it names itself in every answer.
func (c *Client) Check(ctx context.Context) (*model.User, string, error) {
	user, response, err := c.api.GetMe(ctx, "")
	if err != nil {
		return nil, "", translate(response, err)
	}
	return user, response.ServerVersion, nil
}

// OlderThanSupported reports whether a server's version, as it names it in an
// answer, such as 11.7.11.20260901.abc or 11.7.11, is a release older than
// OldestSupported. A version it cannot read is not called older.
func OlderThanSupported(serverVersion string) bool {
	major, minor, ok := majorMinor(serverVersion)
	oldestMajor, oldestMinor, _ := majorMinor(OldestSupported)
	return ok && (major < oldestMajor || major == oldestMajor && minor < oldestMinor)
}

func majorMinor(version string) (int, int, bool) {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, errMajor := strconv.Atoi(parts[0])
	minor, errMinor := strconv.Atoi(parts[1])
	return major, minor, errMajor == nil && errMinor == nil
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
	if response != nil && response.StatusCode > 0 {
		// Something answered with a success, in a form Client4 cannot read: the
		// web app's page, or a proxy's, at an address that is not the API's.
		return fmt.Errorf("%w: %w", ErrNotMattermost, err)
	}
	return fmt.Errorf("could not reach Mattermost: %w", err)
}

// ErrNotMattermost is an answer with a success status that is not what
// Mattermost's API sends: the address is not Mattermost's.
var ErrNotMattermost = errors.New("the address answered, but not as Mattermost's API does; check MM_URL")

// Password login refusals that say what the person should do instead.
var (
	// ErrMFARequired is a login that needs the code of the account's second factor.
	ErrMFARequired = errors.New("the account asks for the code of its second factor")
	// ErrUseSSO is a login of an account made through single sign-on, which
	// has no password in Mattermost.
	ErrUseSSO = errors.New("the account signs in through single sign-on and has no password in Mattermost")
)

// PasswordLogin logs in with a login id, an email address or a username, and
// a password, as Mattermost's login page does, with the code of the account's
// second factor when it has one, and answers with the session token.
// Login, LoginWithMFA.
func PasswordLogin(ctx context.Context, address string, transport http.RoundTripper, loginID, password, mfaCode string) (string, error) {
	api := client4(address, "", transport, RequestTimeout)
	var response *model.Response
	var err error
	if mfaCode == "" {
		_, response, err = api.Login(ctx, loginID, password)
	} else {
		_, response, err = api.LoginWithMFA(ctx, loginID, password, mfaCode)
	}
	if err != nil {
		var appErr *model.AppError
		if errors.As(err, &appErr) {
			switch appErr.Id {
			case "api.user.check_user_mfa.bad_code.app_error":
				return "", ErrMFARequired
			case "api.user.login.use_auth_service.app_error":
				return "", fmt.Errorf("%w: %s", ErrUseSSO, appErr.Message)
			}
			// Not Error's own text, which reads a 401 as a token mm-mcp
			// was started with.
			return "", fmt.Errorf("the login was refused: %s", appErr.Message)
		}
		return "", translate(response, err)
	}
	return api.AuthToken, nil
}
