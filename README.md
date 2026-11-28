# CertPouch

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
  <img src="assets/logo-light.svg" width="300" alt="CertPouch">
</picture>

[日本語](README.ja.md)

**Private certificates, delivered simply.**

CertPouch is a self-hosted tool for private DNS, private PKI, and pull-based certificate distribution. It is a single Go binary that works with your existing private CA.

> **Project status:** Certificate distribution, NSD zone management, and renewal with an existing private CA are implemented. Deployment targets Linux with systemd. Before you switch an existing environment over, run the checks in the [release and acceptance guide (Japanese)](docs/release-and-acceptance.md).

## How it works

1. A central server stores certificates and their private keys.
2. A receiving host authenticates and asks for the certificates it needs. In DNS inventory mode, the server allows a download when the host's source IP matches the certificate name in the inventory. Operators can also allow additional hosts.
3. The pull client validates each bundle, switches to the new version, and rolls back if the service action fails.
4. The server picks up newly published generations without a restart.

See [DNS inventory configuration](docs/dns-inventory.md) and [server publication](docs/server-publication.md).

## Getting started

1. Install the binary. Linux releases include scripts that install or remove it while keeping operational data. See [Linux installation and removal](docs/linux-installation.md).
2. Choose automated Sheets synchronization or manual distribution of an existing certificate. Follow the configuration steps for your chosen workflow in the [first deployment walkthrough (Japanese)](docs/quickstart.md).

### Configuration files

You manage two YAML files. Each one references the TLS, CA, and token files it needs.

| File | Where | Used by | Example |
| --- | --- | --- | --- |
| `server.yaml` | Server | `server`, `pipeline`, `dns ...`, `cert ...` | [server.example.yaml](configs/server.example.yaml) |
| `client.yaml` | Each receiving host | `pull`, `status` | [client.example.yaml](configs/client.example.yaml) |

`server.yaml` holds the distribution, Google Sheets, DNS generation, and CA renewal settings. Credentials, certificates, and the optional runtime revocation list stay in separate protected files. See [Sheets refresh](docs/sheets-fetch.md), [DNS generation](docs/dns-sync.md), [certificate renewal](docs/certificate-renewal.md), and [client revocation](docs/client-revocation.md).

## Features

| Feature | Guides |
| --- | --- |
| Publish existing certificates and deploy them over HTTPS with per-client authentication, per-certificate source IP control, and rollback | [Server setup](docs/server-publication.md) · [Client deployment](docs/client-deployment.md) |
| Generate NSD forward and IPv4 reverse zones from the inventory, review the differences, and apply validated snapshots | [DNS deployment](docs/dns-sync.md) |
| Issue and renew certificates with an existing private CA, optionally on a systemd timer | [Certificate renewal](docs/certificate-renewal.md) |
| Require mutual TLS and reload client revocations at runtime | [Mutual TLS](docs/mtls.md) · [Client revocation](docs/client-revocation.md) |

## CLI

### Everyday commands

| Command | Purpose |
| --- | --- |
| `certpouch configure dns` | Generate DNS and Sheets-fetch configuration interactively or from flags |
| `certpouch configure server` / `certpouch configure client` | Generate the initial distribution configuration |
| `certpouch pipeline --watch` | Synchronize Sheets, NSD, and certificates periodically |
| `certpouch server` | Start the HTTPS distribution server |
| `certpouch pull` | Fetch and deploy certificate updates |
| `certpouch pull --dry-run` | Preview pending updates |
| `certpouch status` | Inspect local certificates and deployment status |

### Manual operations and diagnostics

| Command | Purpose |
| --- | --- |
| `certpouch cert publish` | Register an existing certificate as a new distribution generation |
| `certpouch cert renew` | Issue or renew configured certificates with an existing private CA |
| `certpouch cert status` | Check the expiry and local state of published certificates |
| `certpouch dns fetch` | Refresh the inventory snapshot from Google Sheets |
| `certpouch dns sync --dry-run` | Preview NSD zone files from the inventory snapshot |
| `certpouch dns diff` | Compare a candidate with the active or baseline snapshot |
| `certpouch dns apply` | Activate a validated snapshot on a managed NSD server |

Automated operation needs only `server.yaml`. See [DNS zone generation and activation](docs/dns-sync.md), [publishing certificates](docs/server-publication.md), and [certificate renewal](docs/certificate-renewal.md).

## Documentation and license

- [Current implementation specification](SPEC.md)
- [Detailed specifications (Japanese)](docs/SPEC/README.md)
- [Build, test, and package with Task (Japanese)](docs/release-and-acceptance.md#リリース候補)
- [DNS inventory example (Japanese)](docs/dns-inventory.md#台帳の記入例)
- [Mutual TLS configuration (Japanese)](docs/mtls.md)
- [MIT License](LICENSE)
