# CertPouch

[日本語](README.ja.md)

**Private certificates, delivered simply.**

CertPouch is a self-hosted project for private DNS, private PKI, and pull-based certificate distribution. It is designed as a single Go binary that works with your existing private CA.

**Project status:** The HTTPS distribution server and pull preview are implemented. Automatic deployment is planned.

## How it works

A central server stores certificates and their keys. In DNS inventory mode, an authenticated host can download a certificate when its source IP matches the certificate name in the Google Sheets snapshot. Operators can grant access from additional hosts. The planned pull client will validate each bundle, switch versions, reload or restart the service, and roll back on failure. [DNS inventory configuration](docs/dns-inventory.md)

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
| `certpouch dns sync` | Synchronize DNS zones |
| `certpouch cert renew` | Renew certificates that are due |

The `server` and `pull --dry-run` commands require `--config PATH`. The remaining commands are planned. [Example client configuration](configs/client.example.yaml)

## Documentation and license

- [Project specification](SPEC.md)
- [Detailed specifications (Japanese)](docs/SPEC/README.md)
- [MIT License](LICENSE)
