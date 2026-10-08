package login

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// OAuth for a program on the person's machine (RFC 8252): the person's own
// browser, whichever it is, opens Mattermost's authorization page, where they
// are usually logged in already and approve mm-mcp; Mattermost then sends that
// browser to a callback on 127.0.0.1, where mm-mcp listens, with a one-time
// code; and mm-mcp exchanges the code, with the PKCE verifier only it knows,
// for an access token. No cookie is read and no browser is driven.

// DefaultCallbackPort is the port of the callback an administrator registers
// for mm-mcp: http://127.0.0.1:8766/callback.
const DefaultCallbackPort = 8766

// Client is an OAuth client of Mattermost's that mm-mcp logs in as: one an
// administrator registered, public, with a callback on 127.0.0.1, or one
// mm-mcp registered itself.
type Client struct {
	ID       string `json:"client_id"`
	Callback string `json:"callback"`
}

// ErrNoOAuthClient is the answer when the server lets no client register
// itself and none was given.
var ErrNoOAuthClient = errors.New("the server lets no OAuth client register itself, and no client id was given")

// OAuth is one OAuth login.
type OAuth struct {
	HTTP    *http.Client
	Address string
	// Client is the client to log in as; empty registers one, when the
	// server allows it, on a callback port of the system's choosing.
	Client Client
	// Register is whether the server lets a client register itself.
	Register bool
	// Open opens a page in the person's browser.
	Open func(address string) error
}

// Token is what a login obtained.
type Token struct {
	Access  string
	Refresh string
	// Client is the client it was obtained as, to log in as again.
	Client Client
}

// Login runs the login, and answers once the person approved it in their
// browser, or refused.
func (o OAuth) Login(ctx context.Context) (Token, error) {
	client := o.Client
	port := 0
	if client.Callback != "" {
		parsed, err := url.Parse(client.Callback)
		if err != nil || parsed.Hostname() != "127.0.0.1" {
			return Token{}, fmt.Errorf("the callback %q is not on 127.0.0.1", client.Callback)
		}
		port, _ = strconv.Atoi(parsed.Port())
	}
	if client.ID == "" && !o.Register {
		return Token{}, ErrNoOAuthClient
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return Token{}, fmt.Errorf("listening for the OAuth callback on port %d: %w; another program may be using it", port, err)
	}
	defer func() { _ = listener.Close() }()
	if client.ID == "" {
		bound, ok := listener.Addr().(*net.TCPAddr)
		if !ok {
			return Token{}, fmt.Errorf("listening for the OAuth callback on %s, which is not a TCP address", listener.Addr())
		}
		callback := fmt.Sprintf("http://127.0.0.1:%d/callback", bound.Port)
		if client, err = o.register(ctx, callback); err != nil {
			return Token{}, err
		}
	}
	verifier, challenge := pkce()
	state := randomString(24)
	authorize := o.Address + "/oauth/authorize?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {client.ID},
		"redirect_uri":          {client.Callback},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode()

	codes := make(chan callbackResult, 1)
	server := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: callbackHandler(state, codes)}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown) //nolint:contextcheck // the login has ended; shutting down needs its own deadline
	}()
	if err := o.Open(authorize); err != nil {
		return Token{}, fmt.Errorf("opening the browser: %w; open %s yourself", err, authorize)
	}
	var got callbackResult
	select {
	case got = <-codes:
	case <-ctx.Done():
		return Token{}, ctx.Err()
	}
	if got.err != nil {
		return Token{}, got.err
	}
	token, err := o.exchange(ctx, client, got.code, verifier)
	if err != nil {
		return Token{}, err
	}
	token.Client = client
	return token, nil
}

type callbackResult struct {
	code string
	err  error
}

// callbackHandler takes the browser's visit to the callback, once.
func callbackHandler(state string, codes chan<- callbackResult) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		query := r.URL.Query()
		result := callbackResult{code: query.Get("code")}
		switch {
		case query.Get("state") != state:
			// Not the answer to this login: another page may not end it.
			http.Error(w, "This is not the login mm-mcp started.", http.StatusBadRequest)
			return
		case query.Get("error") != "":
			result.err = fmt.Errorf("mm-mcp was not authorized: %s %s", query.Get("error"), query.Get("error_description"))
		case result.code == "":
			result.err = errors.New("no authorization code came back from Mattermost")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		message := "mm-mcp is logged in. You can close this tab."
		if result.err != nil {
			message = "mm-mcp is not logged in: " + result.err.Error()
		}
		_, _ = fmt.Fprintf(w, "<!doctype html><title>mm-mcp</title><p>%s</p>", html.EscapeString(message))
		select {
		case codes <- result:
		default:
		}
	})
}

// register registers mm-mcp as a public client with callback (RFC 7591), with
// no secret: PKCE protects its codes instead.
func (o OAuth) register(ctx context.Context, callback string) (Client, error) {
	body, _ := json.Marshal(map[string]any{
		"client_name":                "mm-mcp",
		"client_uri":                 "https://github.com/vriesdemichael/mm-mcp",
		"redirect_uris":              []string{callback},
		"token_endpoint_auth_method": "none",
	})
	request, err := newRequest(ctx, http.MethodPost, o.Address+"/api/v4/oauth/apps/register", bytes.NewReader(body))
	if err != nil {
		return Client{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	var registered struct {
		ClientID string `json:"client_id"`
	}
	if err := o.do(request, http.StatusCreated, &registered); err != nil {
		return Client{}, fmt.Errorf("registering mm-mcp as an OAuth client: %w", err)
	}
	return Client{ID: registered.ClientID, Callback: callback}, nil
}

// exchange trades the code for the tokens.
func (o OAuth) exchange(ctx context.Context, client Client, code, verifier string) (Token, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {client.ID},
		"code":          {code},
		"redirect_uri":  {client.Callback},
		"code_verifier": {verifier},
	}
	request, err := newRequest(ctx, http.MethodPost, o.Address+"/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var tokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := o.do(request, http.StatusOK, &tokens); err != nil {
		return Token{}, fmt.Errorf("exchanging the authorization code: %w", err)
	}
	if tokens.AccessToken == "" {
		return Token{}, errors.New("no access token came back for the code")
	}
	return Token{Access: tokens.AccessToken, Refresh: tokens.RefreshToken}, nil
}

func (o OAuth) do(request *http.Request, want int, out any) error {
	response, err := o.HTTP.Do(request) //nolint:gosec // the server the person logs in to, as they gave its address
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != want {
		var problem struct {
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		_ = json.NewDecoder(response.Body).Decode(&problem)
		return fmt.Errorf("the server answered %s: %s%s", response.Status, problem.Message, problem.Error)
	}
	return json.NewDecoder(response.Body).Decode(out)
}

// pkce is a code verifier and its S256 challenge (RFC 7636).
func pkce() (string, string) {
	verifier := randomString(48)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomString(n int) string {
	raw := make([]byte, n)
	_, _ = rand.Read(raw)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// OpenInBrowser opens a page in the person's default browser.
func OpenInBrowser(address string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", address) //nolint:gosec // a page of the server the person logs in to
	case "darwin":
		command = exec.Command("open", address) //nolint:gosec // a page of the server the person logs in to
	default:
		command = exec.Command("xdg-open", address) //nolint:gosec // a page of the server the person logs in to
	}
	return command.Start()
}
