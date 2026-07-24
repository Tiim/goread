package feed

import (
	"testing"
	"time"
)

func TestContentHash_Deterministic(t *testing.T) {
	d := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	a := ContentHash("https://example.com/a", &d)
	b := ContentHash("https://example.com/a", &d)
	if a != b {
		t.Errorf("ContentHash not deterministic: %q != %q", a, b)
	}
}

func TestContentHash_DiffersByLink(t *testing.T) {
	d := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	a := ContentHash("https://example.com/a", &d)
	b := ContentHash("https://example.com/b", &d)
	if a == b {
		t.Errorf("ContentHash should differ for different links")
	}
}

func TestContentHash_DiffersByDate(t *testing.T) {
	d1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	a := ContentHash("https://example.com/a", &d1)
	b := ContentHash("https://example.com/a", &d2)
	if a == b {
		t.Errorf("ContentHash should differ for different dates")
	}
}

func TestContentHash_NilDate(t *testing.T) {
	a := ContentHash("https://example.com/a", nil)
	b := ContentHash("https://example.com/a", nil)
	if a != b {
		t.Errorf("ContentHash with nil date not deterministic")
	}
}
