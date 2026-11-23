package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Surigoma/CertPouch/internal/distribution"
)

// PublicationHandler swaps complete, validated catalogs between HTTP requests.
// Client authorization is fixed for the lifetime of the handler.
type PublicationHandler struct {
	current atomic.Pointer[distribution.Handler]
	update  sync.Mutex
	clients []distribution.Client
	loaded  []publicationDigest
	logger  *slog.Logger
}

func NewPublicationHandler(publications []distribution.Publication, clients []distribution.Client, logger *slog.Logger) (*PublicationHandler, error) {
	if logger == nil {
		logger = slog.Default()
	}
	handler, err := distribution.NewHandler(publications, clients, logger)
	if err != nil {
		return nil, err
	}
	h := &PublicationHandler{clients: clients, loaded: publicationDigests(publications), logger: logger}
	h.current.Store(handler)
	return h, nil
}

func (h *PublicationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.current.Load().ServeHTTP(w, r)
}

func (h *PublicationHandler) refresh(load func() ([]distribution.Publication, error)) error {
	h.update.Lock()
	defer h.update.Unlock()
	publications, err := load()
	if err != nil {
		return fmt.Errorf("load certificate publications: %w", err)
	}
	loaded := publicationDigests(publications)
	if slices.Equal(h.loaded, loaded) {
		return nil
	}
	handler, err := distribution.NewHandler(publications, h.clients, h.logger)
	if err != nil {
		return fmt.Errorf("validate refreshed publications: %w", err)
	}
	h.current.Store(handler)
	h.loaded = loaded
	h.logger.Info("certificate publications refreshed", "count", len(publications))
	return nil
}

func (h *PublicationHandler) Watch(ctx context.Context, interval time.Duration, load func() ([]distribution.Publication, error)) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			if err := h.refresh(load); err != nil {
				h.logger.Error("could not refresh certificate publications", "error", err)
			}
		}
	}
}

type publicationDigest struct {
	name, version           string
	current                 bool
	certificate, chain, key [sha256.Size]byte
}

// The storage loader supplies publications in stable certificate name order.
func publicationDigests(publications []distribution.Publication) []publicationDigest {
	result := make([]publicationDigest, 0, len(publications))
	for _, p := range publications {
		result = append(result, publicationDigest{
			name: p.Name, version: p.Version, current: p.Current,
			certificate: sha256.Sum256(p.Bundle.CertificatePEM), chain: sha256.Sum256(p.Bundle.ChainPEM),
			key: sha256.Sum256(p.Bundle.PrivateKeyPEM),
		})
	}
	return result
}
