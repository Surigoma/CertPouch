package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Surigoma/CertPouch/internal/distribution"
)

// PublicationHandler swaps validated catalog and authorization snapshots between requests.
type PublicationHandler struct {
	current     atomic.Pointer[distribution.Handler]
	update      sync.Mutex
	clients     []distribution.Client
	loaded      []publicationDigest
	logger      *slog.Logger
	revocations *distribution.RevocationPolicy
}

func NewPublicationHandler(publications []distribution.Publication, clients []distribution.Client, logger *slog.Logger) (*PublicationHandler, error) {
	return NewPublicationHandlerWithRevocations(publications, clients, logger, nil)
}

func NewPublicationHandlerWithRevocations(publications []distribution.Publication, clients []distribution.Client, logger *slog.Logger, revocations *distribution.RevocationPolicy) (*PublicationHandler, error) {
	if logger == nil {
		logger = slog.Default()
	}
	handler, err := distribution.NewHandlerWithRevocations(publications, clients, logger, revocations)
	if err != nil {
		return nil, err
	}
	h := &PublicationHandler{clients: clients, loaded: publicationDigests(publications), logger: logger, revocations: revocations}
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
	handler, err := distribution.NewHandlerWithRevocations(publications, h.clients, h.logger, h.revocations)
	if err != nil {
		return fmt.Errorf("validate refreshed publications: %w", err)
	}
	h.current.Store(handler)
	h.loaded = loaded
	h.logger.Info("certificate publications refreshed", "count", len(publications))
	return nil
}

func (h *PublicationHandler) WatchRevocations(ctx context.Context, interval time.Duration, load func() (distribution.Revocations, error)) {
	if h.revocations == nil {
		return
	}
	h.watch(ctx, interval, "client revocation list unavailable; distribution denied", func() error {
		entries, err := load()
		if err != nil {
			h.revocations.Replace(distribution.Revocations{DenyAll: true})
			return err
		}
		h.revocations.Replace(entries)
		return nil
	})
}

func (h *PublicationHandler) Watch(ctx context.Context, interval time.Duration, load func() ([]distribution.Publication, error)) {
	h.watch(ctx, interval, "could not refresh certificate publications", func() error { return h.refresh(load) })
}

// WatchInventory uses a separate refresh cycle so an invalid certificate
// publication cannot prevent revocation of a previous inventory address.
func (h *PublicationHandler) WatchInventory(ctx context.Context, interval time.Duration, load func() ([]distribution.Client, error)) {
	h.watch(ctx, interval, "DNS inventory authorization unavailable", func() error { return h.refreshInventory(load) })
}

func (h *PublicationHandler) refreshInventory(load func() ([]distribution.Client, error)) error {
	clients, loadErr := load()
	h.update.Lock()
	defer h.update.Unlock()
	if loadErr != nil {
		clients = nil
	}
	if reflect.DeepEqual(h.clients, clients) {
		return loadErr
	}
	handler, err := h.current.Load().WithClients(clients)
	if err != nil {
		loadErr = fmt.Errorf("validate new DNS inventory rules: %w", err)
		clients = nil
		handler, err = h.current.Load().WithClients(nil)
		if err != nil {
			return fmt.Errorf("suspend DNS inventory authorization: %w", err)
		}
	}
	h.clients = clients
	h.current.Store(handler)
	if loadErr == nil {
		h.logger.Info("DNS inventory authorization refreshed", "clients", len(clients))
	}
	return loadErr
}

func (h *PublicationHandler) watch(ctx context.Context, interval time.Duration, errorMessage string, refresh func() error) {
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
			if err := refresh(); err != nil {
				h.logger.Error(errorMessage, "error", err)
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
