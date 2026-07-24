package feed

import (
	"testing"
	"time"
)

func TestTTL_RSSExplicitTTL(t *testing.T) {
	const src = `<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <title>Feed</title>
    <link>https://example.com</link>
    <description>d</description>
    <ttl>15</ttl>
  </channel>
</rss>`
	parsed, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got := TTL(parsed); got != 15*time.Minute {
		t.Errorf("TTL() = %v, want 15m", got)
	}
}

func TestTTL_RSSExplicitTTLBelowMinimumIsClamped(t *testing.T) {
	const src = `<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <title>Feed</title>
    <link>https://example.com</link>
    <description>d</description>
    <ttl>1</ttl>
  </channel>
</rss>`
	parsed, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got := TTL(parsed); got != MinTTL {
		t.Errorf("TTL() = %v, want MinTTL (%v)", got, MinTTL)
	}
}

func TestTTL_SyndicationExtension(t *testing.T) {
	const src = `<?xml version="1.0"?>
<rss version="2.0" xmlns:sy="http://purl.org/rss/1.0/modules/syndication/">
  <channel>
    <title>Feed</title>
    <link>https://example.com</link>
    <description>d</description>
    <sy:updatePeriod>daily</sy:updatePeriod>
    <sy:updateFrequency>4</sy:updateFrequency>
  </channel>
</rss>`
	parsed, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	want := 6 * time.Hour // daily / 4
	if got := TTL(parsed); got != want {
		t.Errorf("TTL() = %v, want %v", got, want)
	}
}

func TestTTL_DefaultsWhenAbsent(t *testing.T) {
	const src = `<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <title>Feed</title>
    <link>https://example.com</link>
    <description>d</description>
  </channel>
</rss>`
	parsed, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got := TTL(parsed); got != DefaultTTL {
		t.Errorf("TTL() = %v, want DefaultTTL (%v)", got, DefaultTTL)
	}
}

func TestTTL_AtomDefaultsWhenAbsent(t *testing.T) {
	const src = `<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Feed</title>
  <id>https://example.com/</id>
  <updated>2006-01-02T15:04:05Z</updated>
</feed>`
	parsed, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got := TTL(parsed); got != DefaultTTL {
		t.Errorf("TTL() = %v, want DefaultTTL (%v)", got, DefaultTTL)
	}
}
