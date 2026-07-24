package feed

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// ContentHash computes the fallback identity hash for an article: Link +
// Date, per the spec's GUID -> Link -> content-hash identity chain. It is
// used when a feed item has neither a GUID nor a stable link that can be
// matched directly.
func ContentHash(link string, date *time.Time) string {
	d := ""
	if date != nil {
		d = date.UTC().Format(time.RFC3339)
	}
	sum := sha256.Sum256([]byte(link + "|" + d))
	return hex.EncodeToString(sum[:])
}
