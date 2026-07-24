// Package favicon fetches and downsizes feed favicons for local storage, per
// docs/spec.md "Favicons": try the feed-supplied icon URL first (Atom
// <icon> / RSS <image>, resolved by the caller before calling Fetch), then
// fall back to the site's /favicon.ico.
package favicon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

const (
	maxBytes     = 2 << 20 // 2 MiB
	fetchTimeout = 10 * time.Second
	// targetSize bounds the stored favicon's longest edge, in pixels.
	targetSize = 32
)

var errBlockedHost = errors.New("favicon: host resolves to a private, loopback, or link-local address")

// Result is a fetched (and, where possible, downsized) favicon ready for
// storage.
type Result struct {
	Data        []byte
	ContentType string
}

// Client fetches favicons, rejecting requests to private/loopback/link-local
// network addresses since feed and site URLs are untrusted input (mirrors
// internal/imageproxy's SSRF gate).
type Client struct {
	httpClient   *http.Client
	skipHostGate bool
}

// New creates a favicon-fetching Client.
func New() *Client { return newClient(false) }

// NewForTesting creates a Client with the private/loopback network gate
// disabled, so tests can point it at an httptest server. Never use this
// outside tests.
func NewForTesting() *Client { return newClient(true) }

func newClient(skipHostGate bool) *Client {
	c := &Client{skipHostGate: skipHostGate}
	c.httpClient = &http.Client{
		Timeout: fetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("favicon: too many redirects")
			}
			if c.skipHostGate {
				return nil
			}
			return checkHost(req.URL)
		},
	}
	return c
}

// Fetch tries candidateURL first (the feed's own icon/image URL, already
// resolved by the caller per the spec's priority order), then falls back to
// siteURL's "/favicon.ico". It returns a nil Result and nil error - not an
// error - if neither source yields a usable image, since a missing favicon
// isn't a failure condition for a feed refresh.
func (c *Client) Fetch(ctx context.Context, candidateURL, siteURL string) (*Result, error) {
	for _, u := range candidates(candidateURL, siteURL) {
		data, contentType, err := c.fetchOne(ctx, u)
		if err != nil {
			continue
		}
		return downsize(data, contentType), nil
	}
	return nil, nil
}

func candidates(candidateURL, siteURL string) []string {
	var out []string
	if candidateURL != "" {
		out = append(out, candidateURL)
	}
	if siteURL != "" {
		if u, err := url.Parse(siteURL); err == nil && u.Scheme != "" && u.Host != "" {
			fallback := url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/favicon.ico"}
			out = append(out, fallback.String())
		}
	}
	return out
}

func (c *Client) fetchOne(ctx context.Context, rawURL string) ([]byte, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", fmt.Errorf("favicon: parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, "", fmt.Errorf("favicon: unsupported scheme %q", u.Scheme)
	}
	if !c.skipHostGate {
		if err := checkHost(u); err != nil {
			return nil, "", err
		}
	}

	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", fmt.Errorf("favicon: build request: %w", err)
	}
	req.Header.Set("Accept", "image/*")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("favicon: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("favicon: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return nil, "", fmt.Errorf("favicon: read body: %w", err)
	}
	if len(body) == 0 {
		return nil, "", errors.New("favicon: empty response")
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(body)
	}
	return body, contentType, nil
}

// downsize decodes data as an image and, if decodable, shrinks it to at
// most targetSize on its longest edge and re-encodes it as PNG. Formats the
// standard library can't decode - notably the classic multi-image .ico
// container many sites serve at /favicon.ico - are stored exactly as
// fetched instead: browsers render them fine via <img src>, and the goal
// here is bounding storage size, not universal re-encoding.
func downsize(data []byte, contentType string) *Result {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return &Result{Data: data, ContentType: contentType}
	}

	resized := resize(img, targetSize)
	var buf bytes.Buffer
	if err := png.Encode(&buf, resized); err != nil {
		return &Result{Data: data, ContentType: contentType}
	}
	return &Result{Data: buf.Bytes(), ContentType: "image/png"}
}

// resize scales img down (nearest-neighbor) so its longest edge is at most
// maxDim, preserving aspect ratio. Images already within bounds are
// returned unchanged.
func resize(img image.Image, maxDim int) image.Image {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= maxDim && h <= maxDim {
		return img
	}

	scale := float64(maxDim) / float64(w)
	if hs := float64(maxDim) / float64(h); hs < scale {
		scale = hs
	}
	newW := max(1, int(float64(w)*scale))
	newH := max(1, int(float64(h)*scale))

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	for y := 0; y < newH; y++ {
		srcY := bounds.Min.Y + y*h/newH
		for x := 0; x < newW; x++ {
			srcX := bounds.Min.X + x*w/newW
			dst.Set(x, y, img.At(srcX, srcY))
		}
	}
	return dst
}

func checkHost(u *url.URL) error {
	host := u.Hostname()
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("favicon: resolve host: %w", err)
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
