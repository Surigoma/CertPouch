# CertPouch

[日本語](README.ja.md)

**Private certificates, delivered simply.**

CertPouch is a self-hosted project for private DNS, private PKI, and pull-based certificate distribution. It is designed as a single Go binary that works with your existing private CA.

**Project status:** The HTTPS distribution server, pull preview, and Linux deployment with systemd are implemented. [Linux deployment guide](docs/client-deployment.md)

## How it works

A central server stores certificates and their keys. In DNS inventory mode, an authenticated host can download a certificate when its source IP matches the certificate name in the inventory. Operators can grant access from additional hosts. The pull client validates each bundle, switches versions, and rolls back if service activation fails. The server can refresh published generations without restarting. [DNS inventory configuration](docs/dns-inventory.md) · [Server publication](docs/server-publication.md)

## Roadmap

1. **Certificate distribution.** Add HTTPS, per-client authentication, source IP allowlisting, certificate-level access controls, SHA-256 change detection, and safe deployment. The first integration target is one Keycloak host on Linux.
2. **Private DNS.** Read inventory from Google Sheets and generate, validate, and apply forward and reverse zones for NSD.
3. **Private PKI.** Issue and renew server certificates with the existing private CA and publish validated versions.
4. **Production readiness.** Improve observability, add mTLS, and refine credential revocation and release procedures.

## CLI

| Command | Purpose |
| --- | --- |
| `certpouch server` | Start the HTTPS distribution server |
| `certpouch pull` | Fetch and deploy certificate updates |
| `certpouch pull --dry-run` | Preview pending updates |
| `certpouch status` | Inspect local certificates and deployment status |
| `certpouch cert publish` | Register an existing certificate as a new distribution generation |
| `certpouch dns sync --dry-run` | Preview NSD zone files from the DNS inventory snapshot |
| `certpouch dns fetch` | Refresh the inventory snapshot from Google Sheets |
| `certpouch dns apply` | Activate a validated snapshot on a managed NSD server |
| `certpouch cert renew` | Issue or renew configured certificates using an existing private CA |
| `certpouch cert status` | Check the expiry and local state of published certificates |

Most commands use `--config PATH`. DNS activation uses an explicit candidate and managed directory. Renewal also requires a separate issuer configuration. [DNS zone generation and activation](docs/dns-sync.md) · [Publishing certificates](docs/server-publication.md) · [Certificate renewal](docs/certificate-renewal.md)

## Documentation and license

- [Project specification](SPEC.md)
- [Detailed specifications (Japanese)](docs/SPEC/README.md)
- [Mutual TLS configuration (Japanese)](docs/mtls.md)
- [MIT License](LICENSE)
