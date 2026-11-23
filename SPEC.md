# CertPouch specification

CertPouch is a single Go binary for self-hosted private DNS, private PKI, and HTTPS pull-based certificate distribution. It uses an existing private CA and runs certificate and DNS activation on Linux. The license is MIT.

This document describes the current implementation. [Detailed specifications](docs/SPEC/README.md) define the interfaces and operating behavior. [First deployment](docs/quickstart.md) walks through deploying a single certificate.

## Components and data flow

- Google Sheets, an existing Sheets JSON cache, or CSV exports provide DNS inventory. CertPouch can fetch the Sheets cache with a read-only service account. An existing cache or CSV can be used locally without Google access.
- DNS inventory drives NSD zone generation and, in DNS inventory mode, source IP authorization for distribution.
- Administrators publish an existing certificate or issue one using a configured RSA CA. The distribution server loads validated generations from a local store. The signing key stays with the issuance process.
- Each receiving host retrieves only authorized certificates over HTTPS. It validates the downloaded generation, switches its local `current` link, and performs the locally configured systemd action.
- The CLI lives in `cmd/certpouch`. Internal packages cover YAML configuration, HTTPS server and distribution, the pull client, PKI, DNS, Sheets access, storage, and deployment. The Go requirement and module dependencies are in [go.mod](go.mod).

See the [architecture diagram](docs/SPEC/01-scope-architecture.md#全体構成).

## Command reference

| Command | Behavior |
| --- | --- |
| `certpouch init client` | Generates and validates a client YAML, random token, SHA-256 token hash, and deployment directory under a new output directory on Linux. |
| `certpouch init server` | Generates and validates a server YAML and publication store from the client hash, TLS files, and existing DNS inventory. |
| `certpouch server --config PATH` | Loads validated publications and serves the HTTPS API. `--check-config` validates without opening the listener. |
| `certpouch pull --config PATH` | Compares authorized inventory with local state, downloads updates, validates them, and deploys them. `--dry-run` previews without fetching private keys or making changes. |
| `certpouch status --config PATH` | Reports local deployment and synchronization state, without contacting the server. |
| `certpouch cert publish --config PATH` | Registers a validated leaf certificate, private key and optional intermediate chain. |
| `certpouch cert renew --config PATH --renewal-config PATH` | Issues missing certificates or renews certificates within the configured threshold. Supports `--dry-run`. |
| `certpouch cert status --config PATH` | Checks local published leaf certificates and expiry. |
| `certpouch dns fetch --config PATH` | Fetches and validates a Sheets cache. `--status` reads the local cache state. |
| `certpouch dns sync --config PATH` | Renders a preview with `--dry-run` or creates an NSD-validated candidate with `--stage-dir`. |
| `certpouch dns diff --candidate PATH` | Compares a candidate with a managed `--zone-dir` or a staged `--baseline`. |
| `certpouch dns apply --candidate PATH --zone-dir PATH` | Initializes the managed snapshot with `--initialize`, or updates NSD and verifies the served SOA serials. |

Use subcommand `--help` for the full flag list. Configuration paths are explicit. See the [CLI contract](docs/SPEC/01-scope-architecture.md#cli契約) and [examples for common commands](docs/quickstart.md#普段使うコマンド).

## Configuration and authorization

The YAML loaders reject unknown and duplicate keys. A first distribution setup uses one [server YAML](configs/server.example.yaml) and one [client YAML](configs/client.example.yaml) per receiving host. Optional DNS generation, Sheets fetching, CA renewal, and runtime revocation use their own configuration files.

The receiving host holds the token. The server reads its SHA-256 digest through `clients[].token_hash_file` and compares it in constant time. The client reads the token through `client.token_file`. In DNS inventory mode the server checks the TCP source IP against the certificate's logical name and any explicit per-client certificate exceptions. Static mode uses IP/CIDR allowlists and certificate assignments. Forwarding headers are ignored. Optional mTLS adds client certificate validation and fingerprint binding. Runtime revocation can deny a client ID or mTLS fingerprint.

Use documentation-only addresses and domains in examples. Restrict access to configuration, credentials, and signing keys. See [configuration](docs/SPEC/02-configuration.md), [DNS authorization](docs/dns-inventory.md), [mTLS](docs/mtls.md), and [revocation](docs/client-revocation.md).

## HTTPS distribution API

| Endpoint | Purpose |
| --- | --- |
| `GET /api/v1/health` | Reports server health and the most recent publication refresh result when refresh is enabled. |
| `GET /api/v1/certificates` | Lists authorized current certificates and metadata. |
| `GET /api/v1/certificates/{name}/metadata` | Returns metadata for a certificate's current generation. |
| `GET /api/v1/certificates/{name}/download?version={version}` | Returns the explicitly requested generation and its artifact hashes. |

TLS 1.2 or later is required. The client verifies the server name and configured TLS CA. Distribution requests use a bearer token and client ID. The API returns JSON; the leaf, chain, and private key are separate artifacts. Logs record distribution outcomes without secrets. See the [API contract](docs/SPEC/03-distribution-api.md).

## Certificate validation, deployment, and renewal

Before publication and deployment, CertPouch validates the leaf and key, validity period, DNS/IP SAN set, direct issuer, intermediate chain, and explicit Server Authentication EKU. Distributed keys are RSA, at least 2048 bits, in unencrypted PKCS#1 or PKCS#8 PEM. The CA private key is never part of a distributed bundle.

The server and client keep immutable version directories with atomically switched `current` links. The client verifies the artifact hashes and local policy before switching. An unchanged generation skips file changes and systemd operations. Activation uses only the configured service's `reload` or `restart`. It checks systemd's active state and optionally the fingerprint served on the service's TLS endpoint. Activation failure triggers rollback when an older generation is available. Failed recovery and failed first activation leave a record for manual recovery.

Renewal uses Go's X.509 implementation and an existing RSA CA. It creates a new leaf key, validates the issued bundle, then publishes it. If the CA key uses supported encrypted PEM, OpenSSL decrypts it in memory using a protected file descriptor for the passphrase. No new root CA is generated. See [deployment](docs/SPEC/04-certificate-deployment.md) and [renewal](docs/certificate-renewal.md).

## NSD deployment

CertPouch generates forward and IPv4 reverse candidates from inventory and configured static records. The staging command runs `nsd-checkzone`. The apply command validates the candidate and rollback generations, checks the active serials, atomically switches the snapshot link, reloads affected zones, and confirms the new serials over loopback TCP. Failed activation restores the old records with newer serials when possible. Zone membership changes are configured separately in NSD. See [DNS and private PKI](docs/SPEC/05-dns-pki.md) and the [NSD guide](docs/dns-sync.md).

## Verification, releases, and branding

Local verification uses Go tests and vet, with race testing for shared state. Integration tests use generated test CAs and isolated services. Before migrating an existing deployment, verify the real inventory, NSD configuration, service permissions, CA protection, and backup recovery using the [acceptance guide](docs/release-and-acceptance.md).

GitHub Actions verifies the code. Release Please maintains a release PR, creates a tag and draft release after that PR is merged, and the workflow attaches verified Linux amd64 and arm64 archives and checksums. The draft is published manually.

Brand source assets, generated PNGs, and the MIT license are documented in the [brand specification](docs/SPEC/07-brand.md).