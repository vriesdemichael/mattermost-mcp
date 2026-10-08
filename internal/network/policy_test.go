package network

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// These tests are about HTTP itself: redirects, a 429 and its wait, a
// certificate authority. None claims anything about how Mattermost answers
// (ADR-005); the answers come from a RoundTripper that replays statuses.

func requestTo(t *testing.T, raw string) *http.Request {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Request{URL: parsed}
}

func TestARedirectWithinTheOriginIsFollowed(t *testing.T) {
	t.Parallel()
	first := requestTo(t, "https://chat.example.com/api/v4/users/me")
	if err := SameOriginRedirects(requestTo(t, "https://chat.example.com/other"), []*http.Request{first}); err != nil {
		t.Fatalf("got %v", err)
	}
}

func TestARedirectToAnotherHostOrSchemeIsRefusedAndNamesWhere(t *testing.T) {
	t.Parallel()
	first := requestTo(t, "http://chat.example.com/api/v4/users/me")
	for _, to := range []string{"https://chat.example.com/api/v4/users/me", "http://sso.chat.example.com/login"} {
		err := SameOriginRedirects(requestTo(t, to), []*http.Request{first})
		var redirect *RedirectError
		if !errors.As(err, &redirect) || !strings.Contains(err.Error(), "MM_URL") {
			t.Errorf("%s: got %v", to, err)
		}
	}
}

// replay answers each request with the next status in its list, and counts.
type replay struct {
	statuses []int
	header   http.Header
	sent     int
	bodies   []string
}

func (r *replay) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Body != nil {
		body, _ := io.ReadAll(request.Body)
		r.bodies = append(r.bodies, string(body))
	}
	status := r.statuses[min(r.sent, len(r.statuses)-1)]
	r.sent++
	return &http.Response{StatusCode: status, Header: r.header.Clone(), Body: io.NopCloser(strings.NewReader(""))}, nil
}

func noWait(*http.Request, time.Duration) error { return nil }

func TestA429IsSentAgainWithItsBody(t *testing.T) {
	t.Parallel()
	inner := &replay{statuses: []int{429, 429, 201}, header: http.Header{"Retry-After": {"1"}}}
	transport := &retrying{inner: inner, sleep: noWait}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://chat.example.com/api/v4/posts", strings.NewReader(`{"message":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}

	status, err := statusOf(transport, request)

	if err != nil || status != 201 || inner.sent != 3 {
		t.Fatalf("got %d, %v after %d sends", status, err, inner.sent)
	}
	for _, body := range inner.bodies {
		if body != `{"message":"hi"}` {
			t.Fatalf("a retry sent %q", inner.bodies)
		}
	}
}

func TestA429IsSentAgainAtMostMaxRetriesTimes(t *testing.T) {
	t.Parallel()
	inner := &replay{statuses: []int{429}, header: http.Header{"X-Ratelimit-Reset": {"0"}}}
	transport := &retrying{inner: inner, sleep: noWait}
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://chat.example.com/", nil)

	status, err := statusOf(transport, request)

	if err != nil || status != 429 || inner.sent != MaxRetries+1 {
		t.Fatalf("got %d, %v after %d sends", status, err, inner.sent)
	}
}

func TestA429AskingForALongWaitIsGivenBackAtOnce(t *testing.T) {
	t.Parallel()
	inner := &replay{statuses: []int{429, 200}, header: http.Header{"Retry-After": {"3600"}}}
	transport := &retrying{inner: inner, sleep: noWait}
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://chat.example.com/", nil)

	status, err := statusOf(transport, request)

	if err != nil || status != 429 || inner.sent != 1 {
		t.Fatalf("got %d, %v after %d sends", status, err, inner.sent)
	}
}

func TestAWaitEndsWithTheRequestsContext(t *testing.T) {
	t.Parallel()
	inner := &replay{statuses: []int{429, 200}, header: http.Header{"Retry-After": {"5"}}}
	transport := Retrying(inner)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://chat.example.com/", nil)

	_, err := statusOf(transport, request)

	if err == nil || inner.sent != 1 {
		t.Fatalf("got %v after %d sends", err, inner.sent)
	}
}

func TestTrustCertificatesAddsAnAuthorityAndRefusesWhatIsNone(t *testing.T) {
	t.Parallel()
	transport := NewSafeTransport()
	if err := TrustCertificates(transport, []byte("not a certificate")); err == nil {
		t.Fatal("text that holds no certificate was accepted")
	}
	if err := TrustCertificates(transport, selfSignedPEM(t)); err != nil {
		t.Fatal(err)
	}
	inner := innermost(transport.(*safeTransport).inner).(*http.Transport)
	if inner.TLSClientConfig == nil || inner.TLSClientConfig.RootCAs == nil {
		t.Fatal("the transport trusts no added authority")
	}
}

func selfSignedPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mm-mcp test authority"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// statusOf sends request through transport and gives the answer's status.
func statusOf(transport http.RoundTripper, request *http.Request) (int, error) {
	response, err := transport.RoundTrip(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	return response.StatusCode, nil
}
