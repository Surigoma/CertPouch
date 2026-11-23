package pki

import (
	"fmt"
	"time"
)

// RenewalDue validates the old bundle before using its expiry to select work.
// An expired leaf is checked at the last instant during its validity period.
func RenewalDue(bundle Bundle, policy Policy, now time.Time, threshold time.Duration) (bool, error) {
	leaf, err := DescribeLeaf(bundle.CertificatePEM)
	if err != nil {
		return false, err
	}
	checkAt := now
	if !now.Before(leaf.Leaf.NotAfter) {
		checkAt = leaf.Leaf.NotAfter.Add(-time.Nanosecond)
	}
	if _, err := Validate(bundle, policy, checkAt); err != nil {
		return false, fmt.Errorf("validate current generation: %w", err)
	}
	return !leaf.Leaf.NotAfter.After(now.Add(threshold)), nil
}
