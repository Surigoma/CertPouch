# CertPouch — Project Brand & Development Specification

**Tagline:** Private certificates, delivered simply.  
**Category:** DNS / Private PKI / Certificate Distribution  
**Language:** Go  
**License:** MIT  
**Design direction:** Friendly Infrastructure  
**Project status:** Initial development

---

# Part 1. README.md

# CertPouch

**Private certificates, delivered simply.**

A lightweight, self-hosted DNS and private PKI management system with secure, pull-based certificate distribution.

CertPouch helps you manage private DNS records, issue certificates from your own CA, and distribute them to multiple hosts without relying on external ACME providers.

## Why CertPouch?

Managing certificates across private infrastructure can become tedious.

Certificates are generated on one server, copied to other machines, installed manually, and services must be reloaded whenever certificates change.

CertPouch aims to simplify this workflow.

- Manage private DNS zones from a centralized source.
- Issue certificates using your existing private CA.
- Distribute certificates to authorized hosts using HTTPS.
- Pull certificates on demand or periodically.
- Validate certificates before installation.
- Automatically reload services after successful updates.

## Features

### DNS Management

- Centralized DNS record management.
- Configurable record sources.
- DNS zone file generation.
- NSD integration.
- Automatic zone validation and reload.

### Private PKI

- Bring your own Certificate Authority.
- Generate server certificates with configurable SANs.
- Manage certificate expiration and renewal.
- Support certificate chains.
- No dependency on public ACME providers.

### Certificate Distribution

- Centralized certificate repository.
- Pull-based distribution.
- Host-level certificate access control.
- IP address allowlisting.
- Per-client authentication.
- SHA-256 fingerprint-based change detection.
- Atomic certificate deployment.

### Deployment

- Automatic certificate and private key installation.
- Configurable file ownership and permissions.
- Service reload or restart after successful updates.
- Rollback on deployment failure.

## Architecture

```text
                Configuration Source
                         |
                         v
              +----------------------+
              |    CertPouch Server  |
              |                      |
              |  DNS Management      |
              |  Private PKI         |
              |  Certificate Store  |
              |  Distribution API   |
              +----------------------+
                         |
                    HTTPS / Pull
                         |
          +--------------+--------------+
          |              |              |
          v              v              v
      Host A          Host B          Host C
      Keycloak        Jellyfin        Web Server
          |              |              |
          v              v              v
      Certificate   Certificate    Certificate
      Deployment    Deployment     Deployment
```

## CLI

The planned command interface:

```bash
# Start the management server
certpouch server

# Pull and install certificates
certpouch pull

# Preview pending updates
certpouch pull --dry-run

# Display local certificate status
certpouch status

# Synchronize DNS zones
certpouch dns sync

# Renew certificates
certpouch cert renew
```

The CLI and configuration interfaces are subject to change during initial development.

## Security

CertPouch is designed for private infrastructure.

- HTTPS for all distribution traffic.
- Source IP allowlisting.
- Per-client authorization.
- Private key protection.
- Certificate chain and hostname validation.
- Restricted deployment operations.
- No unrestricted remote command execution.

IP allowlisting is an additional network access control mechanism, not a replacement for cryptographic client authentication.

## Design Principles

1. **Self-hosted first** — No mandatory cloud service dependencies.
2. **Bring your own CA** — Use an existing private PKI.
3. **Pull-based distribution** — Clients retrieve their own certificates.
4. **Small and practical** — Avoid unnecessary infrastructure and dependencies.
5. **Secure by default** — Protect private keys and validate all deployments.
6. **Automation-friendly** — Designed for systemd and unattended operation.

## Roadmap

- [ ] Core Go CLI
- [ ] HTTPS certificate distribution server
- [ ] Pull-based client
- [ ] Client authorization and IP allowlisting
- [ ] Certificate validation and atomic deployment
- [ ] Service reload integration
- [ ] NSD zone management
- [ ] Private CA certificate issuance
- [ ] Certificate expiration monitoring
- [ ] Documentation and deployment examples

## License

MIT License.

---

# Part 2. Logo and Visual Identity

## Brand Identity

**Personality:** Friendly, practical, reliable, approachable.

CertPouch should feel like a small, dependable infrastructure tool rather than an enterprise security platform.

### Logo Concept

Primary visual elements:

- A pouch or compact container representing certificate storage and delivery.
- A certificate document representing TLS and PKI.
- A subtle checkmark or shield representing validation and trust.

Avoid:

- Overly complex padlock illustrations.
- Aggressive cybersecurity aesthetics.
- Excessive gradients or neon effects.
- Designs that resemble an enterprise CA vendor.

### Color Palette

| Role | Color | Hex |
| --- | --- | --- |
| Primary | Deep Navy | `#142D40` |
| Accent | Soft Teal | `#39B8A5` |
| Background | Off White | `#F4F7F7` |
| Secondary | Slate Blue | `#A8C7D2` |
| Text | Dark Slate | `#263746` |

The logo must remain recognizable in monochrome and at small sizes.

### Typography

- Primary: Inter
- Alternative: system sans-serif
- Wordmark: Medium or Semibold
- Avoid overly decorative typefaces.

### Required Assets

```text
assets/
├── logo.svg
├── logo-dark.svg
├── logo-light.svg
├── icon.svg
├── icon-512.png
└── social-preview.png
```

Requirements:

- SVG as the primary source format.
- Square icon for GitHub and application icons.
- Horizontal wordmark for README headers.
- Transparent backgrounds where appropriate.
- Consistent appearance in light and dark themes.

The logo must be original artwork and must not incorporate third-party trademarks.

---

# Part 3. Codex Implementation Specification

## 1. Project Objective

Implement CertPouch as a Go-based, self-hosted DNS and private PKI management system.

The system consists of a central server and a pull-based client.

Both roles must be available through a single Go binary:

```bash
certpouch server
certpouch pull
certpouch status
certpouch dns sync
certpouch cert renew
```

The initial implementation should prioritize certificate distribution while establishing an architecture that can eventually replace the existing Python and shell-based DNS/PKI management system.

Do not implement a full ACME client.

Use an existing private CA.

## 2. Existing Environment and Migration Context

The existing infrastructure has:

- A Python DNS management process.
- Google Sheets as the DNS inventory source.
- NSD as the authoritative DNS server.
- OpenSSL-based certificate generation.
- An existing intermediate CA.
- Certificates and private keys stored centrally.
- Multiple Linux hosts requiring certificate deployment.

The existing Python implementation and certificate generation scripts are migration references.

Do not assume access to production credentials, internal IP addresses, or private keys.

Never commit actual production configuration or credentials.

## 3. Architecture

Use a modular Go architecture.

```text
certpouch/
├── cmd/
│   └── certpouch/
│       └── main.go
├── internal/
│   ├── cli/
│   ├── config/
│   ├── server/
│   ├── client/
│   ├── distribution/
│   ├── pki/
│   ├── dns/
│   ├── storage/
│   └── deployment/
├── configs/
│   ├── server.example.yaml
│   └── client.example.yaml
├── docs/
├── assets/
├── tests/
├── LICENSE
├── README.md
└── go.mod
```

Use Go standard library wherever practical.

Avoid unnecessary dependencies.

Do not introduce Kubernetes, databases, message brokers, or external orchestration systems without a concrete requirement.

## 4. CLI Design

Implement a single executable with subcommands.

### Server

```bash
certpouch server --config /etc/certpouch/server.yaml
```

Responsibilities:

- Load configuration.
- Initialize certificate storage.
- Start HTTPS API.
- Enforce client authorization.
- Expose certificate metadata and distribution endpoints.
- Provide health checks.
- Log distribution operations.

### Pull

```bash
certpouch pull --config /etc/certpouch/client.yaml
```

Responsibilities:

- Connect to the configured server.
- Authenticate the client.
- Retrieve its authorized certificate inventory.
- Compare local and remote fingerprints.
- Download changed certificates.
- Validate certificate and private key consistency.
- Deploy updated files.
- Reload configured services.
- Roll back on failure.

The command must be idempotent.

### Status

```bash
certpouch status --config /etc/certpouch/client.yaml
```

Display:

- Installed certificates.
- Subject and SANs.
- Issuer.
- Expiration.
- Fingerprint.
- Last successful synchronization.
- Current deployment status.

Do not display private key contents.

### DNS

```bash
certpouch dns sync
```

Responsibilities:

- Retrieve configured DNS inventory.
- Generate forward and reverse zones.
- Validate generated zones.
- Apply updates to NSD.
- Maintain correct DNS serial numbers.

### Certificate Management

```bash
certpouch cert renew
```

Responsibilities:

- Identify certificates requiring renewal.
- Generate new certificates using the configured private CA.
- Validate generated artifacts.
- Publish new versions only after successful validation.

## 5. Certificate Distribution API

Use a versioned HTTP API.

Proposed endpoints:

```text
GET  /api/v1/health
GET  /api/v1/certificates
GET  /api/v1/certificates/{name}/metadata
GET  /api/v1/certificates/{name}/download
```

The API must not expose certificates or private keys to unauthorized clients.

Certificate metadata should include:

- Certificate name.
- SHA-256 fingerprint.
- NotBefore.
- NotAfter.
- Subject.
- SANs.
- Issuer.
- Version identifier.

The download response should provide the certificate chain and private key as separate, clearly identified artifacts or as a documented archive format.

Use HTTPS exclusively.

Do not expose private keys through query strings, URLs, logs, or error messages.

## 6. Authentication and Authorization

Implement source IP allowlisting and per-client authentication.

The server configuration must support:

- Client identity.
- Allowed source IP addresses and CIDRs.
- Allowed certificate names.
- Client authentication credentials.
- Credential revocation.

For the initial implementation:

- Require IP allowlisting.
- Require a per-client credential for private key downloads.
- Store credential hashes rather than plaintext tokens on the server.
- Compare credentials using constant-time comparison.
- Keep credentials out of logs.

Keep the authentication implementation extensible so mTLS can be added later.

Do not treat an IP address as cryptographic identity.

Clients must only be able to download certificates explicitly assigned to them.

## 7. Certificate Validation

Before publishing or deploying certificates, verify:

- Certificate PEM parsing.
- Private key PEM parsing.
- Certificate and private key match.
- Certificate validity period.
- Expected SANs.
- Expected issuer.
- Certificate chain validity.
- Server authentication EKU where applicable.

Use Go's `crypto/x509`, `crypto/rsa`, and `crypto/tls`.

Support existing CA certificates.

Do not generate a new root CA automatically.

Do not expose CA private keys to distribution clients.

Do not weaken TLS validation to accommodate private certificates. Configure the appropriate trusted CA bundle instead.

## 8. Safe Deployment

Certificate deployment must be atomic from the perspective of the consuming service.

Recommended structure:

```text
/etc/certpouch/
├── certificates/
│   └── example.internal/
│       ├── versions/
│       │   ├── <version-1>/
│       │   └── <version-2>/
│       └── current -> versions/<version-2>
└── client.yaml
```

Requirements:

- Download to a temporary location.
- Verify checksums.
- Validate all certificate artifacts.
- Set ownership and permissions.
- Ensure certificate files are readable by intended services only.
- Atomically switch the active version.
- Execute an explicitly configured service reload or restart.
- Verify reload success.
- Roll back to the previous version on failure.

Default permissions:

```text
Certificate: 0644
Private key: 0600
Configuration containing credentials: 0600
```

Do not allow arbitrary shell commands from server responses.

Service operations must be defined in local client configuration and restricted to approved systemd units and actions.

## 9. DNS Management

Support the existing DNS management model through adapters.

Initial integration targets:

- Google Sheets inventory source.
- NSD zone generation.
- Forward DNS zones.
- Reverse DNS zones.
- Automatic serial management.
- Zone validation before reload.

Use atomic zone file replacement.

Run `nsd-checkzone` before applying a generated zone.

Only reload NSD after successful validation.

Keep DNS inventory source abstraction separate from zone generation so alternative inventory backends can be added later.

Do not embed the existing production spreadsheet ID or internal DNS records in the public repository.

## 10. Private PKI

The existing private CA must remain the signing authority.

Initial implementation should support integration with the existing OpenSSL-based generation process where necessary.

Subsequently, move certificate issuance to Go's `crypto/x509`.

Certificate profiles must support:

- RSA 2048 initially, matching the existing environment.
- Configurable SANs.
- DNS SANs and IP SANs.
- Server Authentication EKU.
- Configurable validity periods.
- Intermediate CA chains.

The existing CA private key must remain protected.

Never pass CA passphrases as process command-line arguments.

Do not overwrite existing valid certificates until new certificates have passed all validation checks.

## 11. Configuration

Use YAML configuration with strict validation.

Example server configuration:

```yaml
server:
  listen: "0.0.0.0:8443"
  tls:
    certificate: "/etc/certpouch/server.crt"
    private_key: "/etc/certpouch/server.key"
    client_ca: "/etc/certpouch/client-ca.crt"

storage:
  certificate_dir: "/var/lib/certpouch/certificates"

clients:
  - id: "web-server-01"
    allowed_ips:
      - "192.0.2.10/32"
    allowed_certificates:
      - "web.example.internal"
    token_file: "/etc/certpouch/credentials/web-server-01.token"
```

Example client configuration:

```yaml
server:
  url: "https://cert-server.example.internal:8443"
  ca_file: "/etc/ssl/certpouch/ca.crt"

client:
  id: "web-server-01"
  token_file: "/etc/certpouch/client.token"

certificates:
  - name: "web.example.internal"
    destination: "/etc/certpouch/certificates/web.example.internal"
    service:
      unit: "nginx.service"
      action: "reload"
```

Configuration examples must contain documentation-only addresses and domains.

## 12. Logging and Observability

Use structured logging.

Include:

- Client ID.
- Certificate name.
- Operation.
- Result.
- Timestamp.
- Error category.

Never log:

- Private keys.
- API tokens.
- Passphrases.
- Authentication headers.
- Full credential-bearing request bodies.

Provide health and status endpoints.

## 13. Testing Requirements

Implement unit tests and integration tests.

Minimum test coverage:

- Configuration parsing and validation.
- DNS zone generation.
- DNS serial updates.
- Certificate generation.
- Certificate and key matching.
- SAN validation.
- Certificate expiration detection.
- API authorization.
- IP allowlist enforcement.
- Unauthorized certificate access.
- Invalid credential rejection.
- Download integrity.
- Atomic deployment.
- Rollback on deployment failure.
- Service reload failure handling.

Use temporary directories and generated test CAs.

Never use production certificates or private keys in tests.

## 14. Development Strategy

Implement in stages.

### Phase 1 — Distribution MVP

- Single Go binary.
- HTTPS distribution server.
- Client authentication.
- IP allowlisting.
- Certificate metadata API.
- Pull command.
- SHA-256 change detection.
- Safe certificate installation.
- systemd timer examples.

First integration target: one Keycloak server.

Do not rewrite DNS management or certificate issuance yet.

### Phase 2 — DNS Integration

- Google Sheets inventory adapter.
- NSD zone generation.
- Zone validation.
- Serial management.
- NSD reload.

Verify output against the existing Python implementation.

### Phase 3 — PKI Integration

- Existing private CA integration.
- Certificate issuance.
- Renewal scheduling.
- Certificate version management.
- Safe publication.

### Phase 4 — Production Readiness

- Improved observability.
- mTLS support.
- Client revocation.
- Documentation.
- Security review.
- Release automation.

## 15. Coding Rules

- Follow idiomatic Go.
- Prefer small, focused packages.
- Use context cancellation.
- Handle SIGTERM and SIGINT gracefully.
- Avoid global mutable state.
- Avoid unnecessary goroutines.
- Ensure concurrent certificate operations are safe.
- Use atomic filesystem operations where applicable.
- Return meaningful errors.
- Never silently ignore failed certificate operations.
- Keep configuration backward compatibility in mind.
- Add tests for every security-sensitive behavior.

## 16. Codex Initial Task

Before implementation:

1. Inspect the existing repository.
2. Identify existing DNS, PKI, and certificate generation behavior.
3. Propose a Go package structure.
4. Document assumptions and unresolved decisions.
5. Implement Phase 1 only.
6. Add tests and sample configurations.
7. Provide a deployment guide for one Linux host.
8. Do not modify production infrastructure.
9. Do not embed internal domains, IP addresses, credentials, or private key material.
10. Report implementation limitations and remaining security considerations.

Do not implement all phases in one large change.

The initial goal is a working, secure certificate Pull workflow that can be tested against the existing private CA infrastructure.

---

**Project Philosophy**

CertPouch should remain small, understandable, self-hosted, and easy to operate.

It should solve the practical problem of managing certificates across private infrastructure without requiring an unnecessarily complex PKI platform.
