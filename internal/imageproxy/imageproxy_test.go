package imageproxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetch_ServesImage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("fake-jpeg-bytes"))
	}))
	defer upstream.Close()

	c := NewForTesting()
	res, err := c.Fetch(context.Background(), upstream.URL+"/photo.jpg")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	defer res.Body.Close()

	if res.ContentType != "image/jpeg" {
		t.Errorf("ContentType = %q, want image/jpeg", res.ContentType)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(body) != "fake-jpeg-bytes" {
		t.Errorf("body = %q, want fake-jpeg-bytes", body)
	}
}

func TestFetch_RejectsNonImageContentType(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html></html>"))
	}))
	defer upstream.Close()

	c := NewForTesting()
	if _, err := c.Fetch(context.Background(), upstream.URL); err == nil {
		t.Fatal("Fetch() error = nil, want error for non-image content type")
	}
}

func TestFetch_RejectsUnsupportedScheme(t *testing.T) {
	c := New()
	if _, err := c.Fetch(context.Background(), "ftp://example.com/img.png"); err == nil {
		t.Fatal("Fetch() error = nil, want error for unsupported scheme")
	}
}

func TestFetch_TruncatesOversizedResponses(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(make([]byte, MaxImageBytes+1024))
	}))
	defer upstream.Close()

	c := NewForTesting()
	res, err := c.Fetch(context.Background(), upstream.URL)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	defer res.Body.Close()

	n, err := io.Copy(io.Discard, res.Body)
	if err != nil {
		t.Fatalf("Copy() error = %v", err)
	}
	if n != MaxImageBytes {
		t.Errorf("copied %d bytes, want capped at %d", n, MaxImageBytes)
	}
}

func TestFetch_RejectsLoopbackByDefault(t *testing.T) {
	c := New()
	if _, err := c.Fetch(context.Background(), "http://127.0.0.1:1/x.png"); err == nil {
		t.Fatal("Fetch() error = nil, want loopback address rejected")
	} else if !strings.Contains(err.Error(), "private, loopback, or link-local") {
		t.Errorf("Fetch() error = %v, want SSRF-guard error", err)
	}
}

func TestFetch_RejectsLinkLocalMetadataAddress(t *testing.T) {
	c := New()
	if _, err := c.Fetch(context.Background(), "http://169.254.169.254/latest/meta-data/"); err == nil {
		t.Fatal("Fetch() error = nil, want link-local metadata address rejected")
	}
}
