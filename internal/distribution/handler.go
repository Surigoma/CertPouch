// Package distribution provides the authenticated HTTPS certificate API.
package distribution

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type Client struct {
	ID                  string
	Enabled             bool
	TokenHash           [sha256.Size]byte
	AllowedIPs          []netip.Prefix
	AllowedCertificates []string
	// CertificateIPs binds each certificate name to its permitted sources.
	// A non-nil map selects certificate-specific authorization.
	CertificateIPs          map[string][]netip.Prefix
	InventoryExpiresAt      time.Time
	ClientCertificateHashes [][sha256.Size]byte
}

type Handler struct {
	router  *gin.Engine
	catalog catalog
	clients map[string]Client
	logger  *slog.Logger
	now     func() time.Time
}

func NewHandler(publications []Publication, clients []Client, logger *slog.Logger) (*Handler, error) {
	return newHandler(publications, clients, logger, time.Now)
}

func newHandler(publications []Publication, clients []Client, logger *slog.Logger, now func() time.Time) (*Handler, error) {
	c, err := buildCatalog(publications, now())
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return (&Handler{catalog: c, logger: logger, now: now}).WithClients(clients)
}

// WithClients makes an independent authorization snapshot over the same
// immutable certificate catalog. Requests already using h can finish normally.
func (h *Handler) WithClients(clients []Client) (*Handler, error) {
	next := &Handler{catalog: h.catalog, clients: make(map[string]Client), logger: h.logger, now: h.now}
	for _, client := range clients {
		if !namePattern.MatchString(client.ID) || next.clients[client.ID].ID != "" ||
			(client.CertificateIPs == nil && len(client.AllowedIPs) == 0) ||
			(client.Enabled && client.TokenHash == [sha256.Size]byte{}) {
			return nil, fmt.Errorf("invalid or duplicate client configuration")
		}
		client.ClientCertificateHashes = append([][sha256.Size]byte(nil), client.ClientCertificateHashes...)
		if client.CertificateIPs != nil {
			if len(client.AllowedIPs) != 0 || len(client.AllowedCertificates) != 0 {
				return nil, fmt.Errorf("cannot mix address-specific and static assignments")
			}
			rules := make(map[string][]netip.Prefix, len(client.CertificateIPs))
			for name, prefixes := range client.CertificateIPs {
				if !namePattern.MatchString(name) {
					return nil, fmt.Errorf("invalid certificate assignment")
				}
				for _, prefix := range prefixes {
					if !prefix.IsValid() || prefix.Addr().Is4In6() {
						return nil, fmt.Errorf("invalid certificate source prefix")
					}
				}
				rules[name] = append([]netip.Prefix(nil), prefixes...)
			}
			client.CertificateIPs = rules
		}
		client.AllowedIPs = append([]netip.Prefix(nil), client.AllowedIPs...)
		for i, prefix := range client.AllowedIPs {
			if !prefix.IsValid() {
				return nil, fmt.Errorf("invalid client IP prefix")
			}
			if prefix.Addr().Is4In6() {
				if prefix.Bits() < 96 {
					return nil, fmt.Errorf("ambiguous IPv4-mapped client prefix")
				}
				client.AllowedIPs[i] = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96).Masked()
			}
		}
		client.AllowedCertificates = append([]string(nil), client.AllowedCertificates...)
		for _, name := range client.AllowedCertificates {
			if !namePattern.MatchString(name) {
				return nil, fmt.Errorf("invalid client certificate assignment")
			}
		}
		sort.Strings(client.AllowedCertificates)
		for i := 1; i < len(client.AllowedCertificates); i++ {
			if client.AllowedCertificates[i] == client.AllowedCertificates[i-1] {
				return nil, fmt.Errorf("duplicate client certificate assignment")
			}
		}
		next.clients[client.ID] = client
	}
	router, err := next.newRouter()
	if err != nil {
		return nil, err
	}
	next.router = router
	return next, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.router.ServeHTTP(w, r)
}

const (
	healthPath    = "/api/v1/health"
	inventoryPath = "/api/v1/certificates"
	clientKey     = "distribution.client"
)

func (h *Handler) newRouter() (*gin.Engine, error) {
	router := gin.New()
	if err := router.SetTrustedProxies(nil); err != nil {
		return nil, fmt.Errorf("disable trusted proxies: %w", err)
	}
	router.RedirectTrailingSlash = false
	router.RedirectFixedPath = false
	router.Use(h.checkRequest)
	router.GET(healthPath, func(c *gin.Context) {
		writeJSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"})
	})
	router.GET(inventoryPath, h.inventory)
	router.GET(inventoryPath+"/:name/metadata", h.withCertificate(h.metadata))
	router.GET(inventoryPath+"/:name/download", h.withCertificate(h.download))
	router.NoRoute(func(c *gin.Context) {
		if path, ok := strings.CutPrefix(c.Request.URL.Path, inventoryPath+"/"); ok {
			name, action, hasAction := strings.Cut(path, "/")
			if !hasAction || strings.Contains(action, "/") || !validCertificatePath(c.Request, name) {
				writeError(c.Writer, http.StatusBadRequest, "invalid_request")
				return
			}
		}
		writeError(c.Writer, http.StatusNotFound, "not_found")
	})
	return router, nil
}

func (h *Handler) checkRequest(c *gin.Context) {
	w, r := c.Writer, c.Request
	w.Header().Set("Cache-Control", "no-store")
	if r.TLS == nil {
		writeError(w, http.StatusBadRequest, "https_required")
		c.Abort()
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		c.Abort()
		return
	}
	if c.FullPath() == healthPath {
		return
	}
	client, ok := h.authenticate(r)
	if !ok {
		h.logger.Warn("distribution request rejected", "operation", "authenticate", "result", "denied", "error_category", "authentication")
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "unauthorized")
		c.Abort()
		return
	}
	c.Set(clientKey, client)
}

func (h *Handler) inventory(c *gin.Context) {
	client := c.MustGet(clientKey).(Client)
	now := h.now()
	list := []Metadata{}
	for _, name := range client.AllowedCertificates {
		if entry := h.catalog.current[name]; entry.available(now) {
			list = append(list, entry.metadata)
		}
	}
	writeJSON(c.Writer, http.StatusOK, map[string][]Metadata{"certificates": list})
}

func validCertificatePath(r *http.Request, name string) bool {
	return namePattern.MatchString(name) && !strings.Contains(strings.ToLower(r.URL.EscapedPath()), "%2f")
}

func (h *Handler) withCertificate(next func(http.ResponseWriter, *http.Request, string, string, time.Time)) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := c.Param("name")
		if !validCertificatePath(c.Request, name) {
			writeError(c.Writer, http.StatusBadRequest, "invalid_request")
			return
		}
		client := c.MustGet(clientKey).(Client)
		if _, allowed := sort.Find(len(client.AllowedCertificates), func(i int) int {
			return strings.Compare(name, client.AllowedCertificates[i])
		}); !allowed || h.catalog.current[name] == nil {
			writeError(c.Writer, http.StatusNotFound, "not_found")
			return
		}
		next(c.Writer, c.Request, client.ID, name, h.now())
	}
}

func (h *Handler) metadata(w http.ResponseWriter, _ *http.Request, _ string, name string, now time.Time) {
	entry := h.catalog.current[name]
	if !entry.available(now) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, entry.metadata)
}

func (h *Handler) download(w http.ResponseWriter, r *http.Request, clientID, name string, now time.Time) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	versions := query["version"]
	if err != nil || len(query) != 1 || len(versions) != 1 || !versionPattern.MatchString(versions[0]) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	entry := h.catalog.versions[name][versions[0]]
	if !entry.available(now) {
		writeError(w, http.StatusConflict, "version_unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(entry.body)
	if err != nil {
		h.logger.Warn("certificate response interrupted", "client_id", clientID, "certificate_name", name, "operation", "download", "result", "failed", "error_category", "transport")
		return
	}
	h.logger.Info("certificate response written", "client_id", clientID, "certificate_name", name, "operation", "download", "result", "ok")
}

func (h *Handler) authenticate(r *http.Request) (Client, bool) {
	ids, headers := r.Header.Values("X-CertPouch-Client-ID"), r.Header.Values("Authorization")
	if len(ids) != 1 || len(headers) != 1 {
		return Client{}, false
	}
	client, ok := h.clients[ids[0]]
	if !ok || !client.Enabled || (!client.InventoryExpiresAt.IsZero() && !h.now().Before(client.InventoryExpiresAt)) {
		return Client{}, false
	}
	if !matchesClientCertificate(r, client.ClientCertificateHashes) {
		return Client{}, false
	}
	scheme, token, ok := strings.Cut(headers[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || len(token) > 1024 {
		return Client{}, false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) < 32 || strings.ContainsAny(token, "\r\n") {
		return Client{}, false
	}
	digest := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(digest[:], client.TokenHash[:]) != 1 {
		return Client{}, false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return Client{}, false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return Client{}, false
	}
	ip = ip.WithZone("").Unmap()
	if client.CertificateIPs != nil {
		for name, prefixes := range client.CertificateIPs {
			for _, prefix := range prefixes {
				if prefix.Contains(ip) {
					client.AllowedCertificates = append(client.AllowedCertificates, name)
					break
				}
			}
		}
		sort.Strings(client.AllowedCertificates)
		return client, true
	}
	for _, prefix := range client.AllowedIPs {
		if prefix.Contains(ip) {
			return client, true
		}
	}
	return Client{}, false
}

func matchesClientCertificate(r *http.Request, allowed [][sha256.Size]byte) bool {
	if len(allowed) == 0 {
		return true
	}
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 || len(r.TLS.VerifiedChains) == 0 ||
		len(r.TLS.VerifiedChains[0]) == 0 || !r.TLS.PeerCertificates[0].Equal(r.TLS.VerifiedChains[0][0]) {
		return false
	}
	digest := sha256.Sum256(r.TLS.PeerCertificates[0].Raw)
	for _, fingerprint := range allowed {
		if digest == fingerprint {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]map[string]string{
		"error": {"code": code, "message": http.StatusText(status)},
	})
}
