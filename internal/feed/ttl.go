package feed

import (
	"strconv"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/mmcdole/gofeed/rss"
)

// DefaultTTL is used when a feed advertises no refresh interval of its own.
const DefaultTTL = 60 * time.Minute

// MinTTL floors any advertised TTL, so a misconfigured feed cannot force
// refreshes more often than this.
const MinTTL = 5 * time.Minute

// TTL determines how often a feed should be refreshed, preferring the feed's
// own advertised interval — RSS's <ttl> element, or the RSS/Atom Syndication
// extension (sy:updatePeriod / sy:updateFrequency) — and falling back to
// DefaultTTL when neither is present or valid.
func TTL(parsed *gofeed.Feed) time.Duration {
	if d, ok := rssTTL(parsed); ok {
		return clampTTL(d)
	}
	if d, ok := syndicationTTL(parsed); ok {
		return clampTTL(d)
	}
	return DefaultTTL
}

func rssTTL(parsed *gofeed.Feed) (time.Duration, bool) {
	orig, ok := parsed.OriginalFeed().(*rss.Feed)
	if !ok || orig == nil || orig.TTL == "" {
		return 0, false
	}
	minutes, err := strconv.Atoi(strings.TrimSpace(orig.TTL))
	if err != nil || minutes <= 0 {
		return 0, false
	}
	return time.Duration(minutes) * time.Minute, true
}

// syndicationTTL implements the RSS Syndication module
// (https://web.resource.org/rss/1.0/modules/syndication/), which Atom feeds
// also sometimes carry: sy:updatePeriod names a base unit (hourly, daily,
// ...) and sy:updateFrequency is how many times per that unit to refresh.
func syndicationTTL(parsed *gofeed.Feed) (time.Duration, bool) {
	period := strings.ToLower(strings.TrimSpace(extensionValue(parsed, "sy", "updatePeriod")))
	freq := strings.TrimSpace(extensionValue(parsed, "sy", "updateFrequency"))
	if period == "" && freq == "" {
		return 0, false
	}

	base := time.Hour
	switch period {
	case "", "hourly":
		base = time.Hour
	case "daily":
		base = 24 * time.Hour
	case "weekly":
		base = 7 * 24 * time.Hour
	case "monthly":
		base = 30 * 24 * time.Hour
	case "yearly":
		base = 365 * 24 * time.Hour
	default:
		return 0, false
	}

	n := 1
	if freq != "" {
		v, err := strconv.Atoi(freq)
		if err != nil || v <= 0 {
			return 0, false
		}
		n = v
	}
	return base / time.Duration(n), true
}

func extensionValue(parsed *gofeed.Feed, namespace, name string) string {
	if parsed.Extensions == nil {
		return ""
	}
	ns, ok := parsed.Extensions[namespace]
	if !ok {
		return ""
	}
	vals, ok := ns[name]
	if !ok || len(vals) == 0 {
		return ""
	}
	return vals[0].Value
}

func clampTTL(d time.Duration) time.Duration {
	if d < MinTTL {
		return MinTTL
	}
	return d
}
