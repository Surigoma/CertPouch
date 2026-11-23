# CertPouch

[日本語](README.ja.md)

**Private certificates, delivered simply.**

CertPouch is a self-hosted project for private DNS, private PKI, and pull-based certificate distribution. It is designed as a single Go binary that works with your existing private CA.

**Project status:** Specification stage. The CLI and features below are planned.

## How it works

A central server stores certificates and their keys. Each host authenticates over HTTPS and pulls only the certificates assigned to it. Before installation, the client checks the certificate chain, expected names, validity period, and matching private key. It switches to the validated version, reloads or restarts the configured service, and rolls back if that operation fails.

## Roadmap

1. **Certificate distribution.** Add HTTPS, per-client authentication, source IP allowlisting, certificate-level access controls, SHA-256 change detection, and safe deployment. The first integration target is one Keycloak host on Linux.
2. **Private DNS.** Read inventory from Google Sheets and generate, validate, and apply forward and reverse zones for NSD.
3. **Private PKI.** Issue and renew server certificates with the existing private CA and publish validated versions.
4. **Production readiness.** Improve observability, add mTLS, and refine credential revocation and release procedures.

## Planned CLI

| Command | Purpose |
| --- | --- |
| `certpouch server` | Start the HTTPS distribution server |
| `certpouch pull` | Fetch and deploy certificate updates |
| `certpouch pull --dry-run` | Preview pending updates |
| `certpouch status` | Inspect local certificates and deployment status |
| `certpouch dns sync` | Synchronize DNS zones |
| `certpouch cert renew` | Renew certificates that are due |

The CLI and configuration may change during initial development.

## Documentation and license

- [Project specification](SPEC.md)
- [Detailed specifications (Japanese)](docs/SPEC/README.md)
- [MIT License](LICENSE)
