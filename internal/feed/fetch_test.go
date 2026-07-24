package feed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetcher_Fetch_ReturnsBodyAndCacheHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != UserAgent {
			t.Errorf("User-Agent = %q, want %q", got, UserAgent)
		}
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		w.Write([]byte("<rss></rss>"))
	}))
	defer srv.Close()

	f := NewFetcher()
	res, err := f.Fetch(context.Background(), srv.URL, "", "")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if res.NotModified {
		t.Fatalf("NotModified = true, want false")
	}
	if string(res.Body) != "<rss></rss>" {
		t.Errorf("Body = %q", res.Body)
	}
	if res.ETag != `"abc"` {
		t.Errorf("ETag = %q", res.ETag)
	}
	if res.LastModified != "Mon, 02 Jan 2006 15:04:05 GMT" {
		t.Errorf("LastModified = %q", res.LastModified)
	}
}

func TestFetcher_Fetch_SendsConditionalHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("If-None-Match"); got != `"abc"` {
			t.Errorf("If-None-Match = %q, want %q", got, `"abc"`)
		}
		if got := r.Header.Get("If-Modified-Since"); got != "Mon, 02 Jan 2006 15:04:05 GMT" {
			t.Errorf("If-Modified-Since = %q", got)
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	f := NewFetcher()
	res, err := f.Fetch(context.Background(), srv.URL, `"abc"`, "Mon, 02 Jan 2006 15:04:05 GMT")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if !res.NotModified {
		t.Errorf("NotModified = false, want true")
	}
}

func TestFetcher_Fetch_PermanentRedirectReportsFinalURL(t *testing.T) {
	var targetURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetURL+"/new", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/new", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<rss></rss>"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	targetURL = srv.URL

	f := NewFetcher()
	res, err := f.Fetch(context.Background(), srv.URL+"/old", "", "")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if res.FinalURL != srv.URL+"/new" {
		t.Errorf("FinalURL = %q, want %q", res.FinalURL, srv.URL+"/new")
	}
	if string(res.Body) != "<rss></rss>" {
		t.Errorf("Body = %q", res.Body)
	}
}

func TestFetcher_Fetch_TemporaryRedirectDoesNotReportFinalURL(t *testing.T) {
	var targetURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetURL+"/new", http.StatusFound)
	})
	mux.HandleFunc("/new", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<rss></rss>"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	targetURL = srv.URL

	f := NewFetcher()
	res, err := f.Fetch(context.Background(), srv.URL+"/old", "", "")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if res.FinalURL != "" {
		t.Errorf("FinalURL = %q, want empty for a temporary redirect", res.FinalURL)
	}
}

func TestFetcher_Fetch_ErrorStatusReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	f := NewFetcher()
	if _, err := f.Fetch(context.Background(), srv.URL, "", ""); err == nil {
		t.Fatal("Fetch() error = nil, want error for 500 response")
	}
}
