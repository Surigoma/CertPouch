package dns

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const CacheFreshnessSuffix = ".freshness.json"

var ErrStaleCache = errors.New("DNS cache exceeds the configured maximum age")

type CacheFreshness struct {
	FetchedAt time.Time `json:"fetched_at"`
	SHA256    string    `json:"sha256"`
}

func newCacheFreshness(body []byte, now time.Time) CacheFreshness {
	digest := sha256.Sum256(body)
	return CacheFreshness{FetchedAt: now.UTC(), SHA256: hex.EncodeToString(digest[:])}
}

// VerifyCacheFreshness binds the last successful fetch time to the exact
// snapshot being read. A partial write or out-of-band edit cannot renew it.
func VerifyCacheFreshness(snapshot, record []byte, now time.Time, maxAge time.Duration) (CacheFreshness, error) {
	var state CacheFreshness
	if json.Unmarshal(record, &state) != nil || state.FetchedAt.IsZero() ||
		state.SHA256 != newCacheFreshness(snapshot, now).SHA256 {
		return CacheFreshness{}, fmt.Errorf("DNS cache does not match its fetch record")
	}
	if state.FetchedAt.After(now) {
		return CacheFreshness{}, fmt.Errorf("DNS cache fetch time is in the future")
	}
	if maxAge > 0 && now.Sub(state.FetchedAt) >= maxAge {
		return state, ErrStaleCache
	}
	return state, nil
}
