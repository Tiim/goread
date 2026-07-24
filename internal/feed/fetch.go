package feed

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// UserAgent identifies GoRead when fetching feeds over HTTP.
const UserAgent = "GoRead/1.0 (+https://github.com/Tiim/goread)"

// maxRedirects bounds how many hops Fetch will follow before giving up.
const maxRedirects = 10

// FetchResult is the outcome of fetching a feed URL.
type FetchResult struct {
	// NotModified is true when the server responded 304 Not Modified; Body,
	// ETag, and LastModified are unset in that case, and the caller should
	// keep whatever it already had stored.
	NotModified  bool
	Body         []byte
	ETag         string
	LastModified string
	// FinalURL is set when a permanent redirect (301/308) was followed
	// anywhere in the chain; per spec, callers should persist it as the
	// feed's new URL.
	FinalURL string
}

// Fetcher retrieves feed content over HTTP, respecting conditional-request
// caching headers and permanent redirects.
type Fetcher struct {
	Client *http.Client
}

// NewFetcher creates a Fetcher with a sane request timeout.
func NewFetcher() *Fetcher {
	return &Fetcher{Client: &http.Client{Timeout: 30 * time.Second}}
}

// Fetch retrieves feedURL, sending If-None-Match/If-Modified-Since headers
// when etag/lastModified are non-empty. Redirects are followed manually
// (rather than via the http.Client default) so permanent redirects can be
// detected and reported back for persistence, per spec.
func (f *Fetcher) Fetch(ctx context.Context, feedURL, etag, lastModified string) (*FetchResult, error) {
	base := f.Client
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	currentURL := feedURL
	var permanentTarget string
	for redirects := 0; ; redirects++ {
		if redirects >= maxRedirects {
			return nil, fmt.Errorf("too many redirects fetching %s", feedURL)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, currentURL, nil)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("User-Agent", UserAgent)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		if lastModified != "" {
			req.Header.Set("If-Modified-Since", lastModified)
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", currentURL, err)
		}

		if isRedirect(resp.StatusCode) {
			loc := resp.Header.Get("Location")
			reqURL := resp.Request.URL
			resp.Body.Close()
			if loc == "" {
				return nil, fmt.Errorf("redirect from %s missing Location header", currentURL)
			}
			target, err := reqURL.Parse(loc)
			if err != nil {
				return nil, fmt.Errorf("resolve redirect location: %w", err)
			}
			if resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusPermanentRedirect {
				permanentTarget = target.String()
			}
			currentURL = target.String()
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response body: %w", err)
		}

		if resp.StatusCode == http.StatusNotModified {
			return &FetchResult{NotModified: true, FinalURL: permanentTarget}, nil
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("fetch %s: unexpected status %s", currentURL, resp.Status)
		}

		return &FetchResult{
			Body:         body,
			ETag:         resp.Header.Get("ETag"),
			LastModified: resp.Header.Get("Last-Modified"),
			FinalURL:     permanentTarget,
		}, nil
	}
}

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}
