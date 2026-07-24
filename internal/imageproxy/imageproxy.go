// Package imageproxy fetches remote images on behalf of the browser so that
// article content never causes the browser to contact third-party hosts
// directly (see docs/spec.md "Images"). Fetched images are streamed straight
// through to the response and are never written to disk or cached
// permanently.
package imageproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MaxImageBytes caps how much of a remote response we will relay, so a
// misbehaving or malicious server can't exhaust memory/bandwidth.
const MaxImageBytes = 10 << 20 // 10 MiB

const fetchTimeout = 10 * time.Second

var errBlockedHost = errors.New("imageproxy: host resolves to a private, loopback, or link-local address")

// Client fetches remote images, rejecting requests to private network
// addresses to prevent the server from being used as an SSRF pivot against
// the local network (feed content is untrusted input).
type Client struct {
	httpClient   *http.Client
	skipHostGate bool
}

// New creates an image-fetching Client.
func New() *Client {
	return newClient(false)
}

// NewForTesting creates a Client with the private/loopback network gate
// disabled, so tests can point it at an httptest server (which binds to
// 127.0.0.1). Never use this outside tests.
func NewForTesting() *Client {
	return newClient(true)
}

func newClient(skipHostGate bool) *Client {
	c := &Client{skipHostGate: skipHostGate}
	c.httpClient = &http.Client{
		Timeout: fetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("imageproxy: too many redirects")
			}
			if c.skipHostGate {
				return nil
			}
			return checkHost(req.URL)
		},
	}
	return c
}

// Result is a successfully fetched remote image, ready to be streamed to the
// browser.
type Result struct {
	ContentType string
	Body        io.ReadCloser
}

// Fetch retrieves rawURL and returns its body if, and only if, the server
// responded with an image content type. Callers must close Result.Body.
func (c *Client) Fetch(ctx context.Context, rawURL string) (*Result, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("imageproxy: parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("imageproxy: unsupported scheme %q", u.Scheme)
	}
	if !c.skipHostGate {
		if err := checkHost(u); err != nil {
			return nil, err
		}
	}

	// The timeout must keep bounding the request while the caller streams
	// the body, so cancel is tied to Result.Body.Close rather than
	// deferred here.
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("imageproxy: build request: %w", err)
	}
	req.Header.Set("Accept", "image/*")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("imageproxy: fetch: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("imageproxy: unexpected status %d", resp.StatusCode)
	}
	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "image/") {
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("imageproxy: non-image content type %q", contentType)
	}

	return &Result{
		ContentType: contentType,
		Body: &cancelOnCloseBody{
			ReadCloser: io.NopCloser(io.LimitReader(resp.Body, MaxImageBytes)),
			cancel:     cancel,
		},
	}, nil
}

// cancelOnCloseBody cancels the request's context once the caller is done
// reading, instead of the moment Fetch returns.
type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnCloseBody) Close() error {
	defer b.cancel()
	return b.ReadCloser.Close()
}

// checkHost rejects URLs whose host resolves to a private, loopback,
// link-local, or otherwise non-public address, so remote feed content can't
// trick the server into probing the local network or cloud metadata
// endpoints.
func checkHost(u *url.URL) error {
	host := u.Hostname()
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("imageproxy: resolve host: %w", err)
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return errBlockedHost
		}
	}
	return nil
}

func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast()
}
