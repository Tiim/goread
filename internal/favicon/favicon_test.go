package favicon

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

func TestFetch_UsesCandidateURL(t *testing.T) {
	data := pngBytes(t, 16, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(data)
	}))
	defer srv.Close()

	c := NewForTesting()
	res, err := c.Fetch(context.Background(), srv.URL+"/icon.png", "")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if res == nil {
		t.Fatal("Fetch() = nil, want a result")
	}
	if res.ContentType != "image/png" {
		t.Errorf("ContentType = %q, want image/png", res.ContentType)
	}
}

func TestFetch_FallsBackToFaviconICO(t *testing.T) {
	data := pngBytes(t, 16, 16)
	var hitPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPath = r.URL.Path
		w.Header().Set("Content-Type", "image/png")
		w.Write(data)
	}))
	defer srv.Close()

	c := NewForTesting()
	res, err := c.Fetch(context.Background(), "", srv.URL)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if res == nil {
		t.Fatal("Fetch() = nil, want a result")
	}
	if hitPath != "/favicon.ico" {
		t.Errorf("requested path = %q, want /favicon.ico", hitPath)
	}
}

func TestFetch_CandidateFailureFallsThrough(t *testing.T) {
	data := pngBytes(t, 8, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			w.Header().Set("Content-Type", "image/png")
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewForTesting()
	res, err := c.Fetch(context.Background(), srv.URL+"/missing.png", srv.URL)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if res == nil {
		t.Fatal("Fetch() = nil, want fallback result")
	}
}

func TestFetch_NoUsableSourceReturnsNilNotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewForTesting()
	res, err := c.Fetch(context.Background(), "", srv.URL)
	if err != nil {
		t.Fatalf("Fetch() error = %v, want nil", err)
	}
	if res != nil {
		t.Errorf("Fetch() = %+v, want nil result", res)
	}
}

func TestFetch_DownsizesLargeImage(t *testing.T) {
	data := pngBytes(t, 256, 128)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(data)
	}))
	defer srv.Close()

	c := NewForTesting()
	res, err := c.Fetch(context.Background(), srv.URL+"/icon.png", "")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	img, _, err := image.Decode(bytes.NewReader(res.Data))
	if err != nil {
		t.Fatalf("decode downsized result: %v", err)
	}
	b := img.Bounds()
	if b.Dx() > targetSize || b.Dy() > targetSize {
		t.Errorf("downsized image = %dx%d, want both dimensions <= %d", b.Dx(), b.Dy(), targetSize)
	}
	if b.Dx() != targetSize {
		t.Errorf("downsized width = %d, want %d (longest edge scaled to target)", b.Dx(), targetSize)
	}
}

func TestFetch_UndecodableImageStoredAsIs(t *testing.T) {
	raw := []byte{0x00, 0x00, 0x01, 0x00, 'n', 'o', 't', 'a', 'r', 'e', 'a', 'l', 'i', 'c', 'o'}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/x-icon")
		w.Write(raw)
	}))
	defer srv.Close()

	c := NewForTesting()
	res, err := c.Fetch(context.Background(), srv.URL+"/favicon.ico", "")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if !bytes.Equal(res.Data, raw) {
		t.Errorf("Data = %v, want raw bytes unchanged when undecodable", res.Data)
	}
	if res.ContentType != "image/x-icon" {
		t.Errorf("ContentType = %q, want image/x-icon", res.ContentType)
	}
}
