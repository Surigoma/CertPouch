package distribution

import (
	"crypto/sha256"
	"net/http"
	"sync/atomic"
)

type Revocations struct {
	ClientIDs         []string
	CertificateHashes [][sha256.Size]byte
	DenyAll           bool
}

type revocationSnapshot struct {
	clients      map[string]struct{}
	certificates map[[sha256.Size]byte]struct{}
	denyAll      bool
}

// RevocationPolicy holds an independent, deny-only overlay shared by
// successive publication and DNS authorization snapshots.
type RevocationPolicy struct {
	current atomic.Pointer[revocationSnapshot]
}

func (p *RevocationPolicy) Replace(list Revocations) {
	next := &revocationSnapshot{
		clients:      make(map[string]struct{}, len(list.ClientIDs)),
		certificates: make(map[[sha256.Size]byte]struct{}, len(list.CertificateHashes)),
		denyAll:      list.DenyAll,
	}
	for _, id := range list.ClientIDs {
		next.clients[id] = struct{}{}
	}
	for _, fingerprint := range list.CertificateHashes {
		next.certificates[fingerprint] = struct{}{}
	}
	p.current.Store(next)
}

func (p *RevocationPolicy) denies(id string, request *http.Request) bool {
	if p == nil {
		return false
	}
	state := p.current.Load()
	if state == nil || state.denyAll {
		return true
	}
	if _, revoked := state.clients[id]; revoked {
		return true
	}
	if len(state.certificates) == 0 || request.TLS == nil || len(request.TLS.PeerCertificates) == 0 {
		return false
	}
	digest := sha256.Sum256(request.TLS.PeerCertificates[0].Raw)
	_, revoked := state.certificates[digest]
	return revoked
}
