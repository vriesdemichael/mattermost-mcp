package network

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// RedirectError is a redirect refused because it leaves the origin the request
// went to. A credential travels in a header, and Go's client forwards it to a
// subdomain; a redirect from http to https turns a POST into a GET, so a write
// would fail in a way that names neither.
type RedirectError struct{ From, To string }

func (e *RedirectError) Error() string {
	return fmt.Sprintf("%s redirected to %s, another address; set MM_URL to the address Mattermost is served at, such as %s",
		e.From, e.To, e.To)
}

// SameOriginRedirects is an http.Client's CheckRedirect that follows a
// redirect only within the scheme and host the first request went to.
func SameOriginRedirects(request *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	first := via[0].URL
	if request.URL.Scheme != first.Scheme || request.URL.Host != first.Host {
		return &RedirectError{
			From: first.Scheme + "://" + first.Host,
			To:   request.URL.Scheme + "://" + request.URL.Host,
		}
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

// Retry limits for an answer of 429 Too Many Requests.
const (
	// MaxRetries is how many times one request is sent again after a 429.
	MaxRetries = 3
	// MaxRetryWait is the longest one wait before sending again: a server that
	// asks for longer is answered with its 429.
	MaxRetryWait = 10 * time.Second
	// defaultRetryWait is the wait when the answer names none.
	defaultRetryWait = time.Second
)

type retrying struct {
	inner http.RoundTripper
	sleep func(*http.Request, time.Duration) error
}

// Retrying sends a request again when the answer is 429 Too Many Requests,
// after the wait the answer asks for in Retry-After or X-Ratelimit-Reset, at
// most MaxRetries times and never longer than MaxRetryWait at once. A request
// whose body cannot be read again is not retried.
func Retrying(inner http.RoundTripper) http.RoundTripper {
	return &retrying{inner: inner, sleep: sleepFor}
}

func sleepFor(request *http.Request, wait time.Duration) error {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-request.Context().Done():
		return request.Context().Err()
	}
}

func (r *retrying) RoundTrip(request *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		response, err := r.inner.RoundTrip(request)
		if err != nil || response.StatusCode != http.StatusTooManyRequests || attempt == MaxRetries {
			return response, err
		}
		wait, ok := retryWait(response.Header)
		if !ok {
			return response, nil
		}
		if request.Body != nil && request.Body != http.NoBody {
			if request.GetBody == nil {
				return response, nil
			}
			body, bodyErr := request.GetBody()
			if bodyErr != nil {
				return response, nil //nolint:nilerr // the 429 is the answer to give
			}
			request = request.Clone(request.Context())
			request.Body = body
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		_ = response.Body.Close()
		if err := r.sleep(request, wait); err != nil {
			return nil, err
		}
	}
}

// retryWait is how long a 429 asks to wait, and whether that is short enough
// to wait for. Mattermost's own limiter sets X-Ratelimit-Reset in seconds; a
// proxy in front of it may set Retry-After instead.
func retryWait(header http.Header) (time.Duration, bool) {
	for _, name := range []string{"Retry-After", "X-Ratelimit-Reset"} {
		raw := header.Get(name)
		if raw == "" {
			continue
		}
		if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
			wait := time.Duration(seconds) * time.Second
			if wait == 0 {
				wait = defaultRetryWait
			}
			return wait, wait <= MaxRetryWait
		}
		if at, err := http.ParseTime(raw); err == nil {
			wait := time.Until(at)
			if wait <= 0 {
				wait = defaultRetryWait
			}
			return wait, wait <= MaxRetryWait
		}
	}
	return defaultRetryWait, true
}

// TrustCertificates makes transport, as NewSafeTransport built it, trust the
// certificate authorities in pem beside the system's own, for a Mattermost
// whose certificate an organisation's own authority signed.
func TrustCertificates(transport http.RoundTripper, pem []byte) error {
	safe, ok := transport.(*safeTransport)
	if !ok {
		return errors.New("the transport is not one NewSafeTransport built")
	}
	inner, ok := innermost(safe.inner).(*http.Transport)
	if !ok {
		return errors.New("the transport has no TLS settings to add a certificate authority to")
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return errors.New("it holds no PEM-encoded certificate")
	}
	if inner.TLSClientConfig == nil {
		inner.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	inner.TLSClientConfig.RootCAs = pool
	return nil
}

func innermost(transport http.RoundTripper) http.RoundTripper {
	if r, ok := transport.(*retrying); ok {
		return innermost(r.inner)
	}
	return transport
}
