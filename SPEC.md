# CertPouch specification

CertPouch is a single Go binary for self-hosted private DNS, private PKI, and HTTPS pull-based certificate distribution. It uses an existing private CA and runs certificate and DNS activation on Linux. The license is MIT.

The target scope below defines the intended workflow. Subsequent sections and the [detailed specifications](docs/SPEC/README.md) describe the current implementation. [First deployment](docs/quickstart.md) walks through deploying a single certificate.

## Target scope

An operator registers a hostname, IP address, and optional aliases in Google Sheets. That is the routine input for both DNS and certificate issuance. Adding a host requires no per-host YAML changes or issuance flag.

1. Poll Sheets at a configurable interval, targeting reflection within a few minutes in normal operation. Account for API quotas and back off on quota errors and transient failures. A future webhook may trigger the same synchronization sooner.
2. Generate DNS records and update NSD. Confirm DNS activation before reconciling certificate issuance, renewal, and availability against the accepted inventory.
3. Use an existing CA and a shared issuance policy. Issue a separate certificate and private key for each hostname and each alias. Authorized receiving servers pull the names they need over HTTPS. Local placement can optionally trigger a configured service action or command.
4. After DNS removal is confirmed, stop new retrieval and automatic renewal for the removed name. Previously deployed certificates may remain in service until expiry. Immediate revocation, such as for a compromised key, is a separate operation.

Inventory membership identifies issuance targets. Retrieval still requires client authentication, source IP authorization, and permission for the requested name.

### Current implementation gap

The current CLI supports Sheets fetch, DNS candidate generation and explicit NSD activation, configured issuance and renewal, and authenticated pull. It still requires configured certificate targets and renewal profiles. The automatic inventory-to-issuance workflow, including stopping retrieval and renewal after confirmed DNS removal, remains target behavior. The current operational examples describe the separate commands.

## Components and data flow

- Google Sheets, an existing Sheets JSON cache, or CSV exports provide DNS inventory. CertPouch can fetch the Sheets cache with a read-only service account. An existing cache or CSV can be used locally without Google access.
- DNS inventory drives NSD zone generation and, in DNS inventory mode, source IP authorization for distribution.
- Administrators publish an existing certificate or issue one using a configured RSA CA. The distribution server loads validated generations from a local store. The signing key stays with the issuance process.
- Each receiving host retrieves only authorized certificates over HTTPS. It validates the downloaded generation, switches its local `current` link, and runs the systemd action or command configured on that host.
- The CLI lives in `cmd/certpouch`. Internal packages cover YAML configuration, HTTPS server and distribution, the pull client, PKI, DNS, Sheets access, storage, and deployment. The Go requirement and module dependencies are in [go.mod](go.mod).

See the [architecture diagram](docs/SPEC/01-scope-architecture.md#全体構成).

## Command reference

| Command | Behavior |
| --- | --- |
| `install.sh` / `uninstall.sh` | On Linux, installs or removes the binary while preserving configuration, credentials, certificates, state, and systemd units. |
| `certpouch configure dns` | Creates validated DNS and optional Sheets-fetch configuration from flags or interactive prompts without overwriting existing files. |
| `certpouch init client` | Generates and validates a client YAML, random token, SHA-256 token hash, and deployment directory under a new output directory on Linux. |
| `certpouch init server` | Generates and validates a server YAML and publication store from the client hash, TLS files, and existing DNS inventory. |
| `certpouch server --config PATH` | Loads validated publications and serves the HTTPS API. `--check-config` validates without opening the listener. |
| `certpouch pull --config PATH` | Compares authorized inventory with local state, downloads updates, validates them, and deploys them. `--dry-run` previews without fetching private keys or making changes. |
| `certpouch status --config PATH` | Reports local deployment and synchronization state, without contacting the server. |
| `certpouch cert publish --config PATH` | Registers a validated leaf certificate, private key and optional intermediate chain. |
| `certpouch cert renew --config PATH` | Issues missing certificates or renews certificates using the renewal section in `server.yaml`. Supports `--dry-run`. |
| `certpouch cert status --config PATH` | Checks local published leaf certificates and expiry. |
| `certpouch dns fetch --config PATH` | Fetches and validates a Sheets cache. `--status` reads the local cache state. |
| `certpouch pipeline --config PATH` | Refreshes the Sheets cache and prints the path to a validated DNS candidate. |
| `certpouch pipeline --config PATH --apply CANDIDATE` | Applies the selected candidate to initialized NSD deployment, rejecting record removals and replacements. |
| `certpouch dns sync --config PATH` | Renders a preview with `--dry-run` or creates an NSD-validated candidate with `--stage-dir`. |
| `certpouch dns diff --candidate PATH` | Compares a candidate with a managed `--zone-dir` or a staged `--baseline`. |
| `certpouch dns apply --candidate PATH --zone-dir PATH` | Initializes the managed snapshot with `--initialize`, or updates NSD and verifies the served SOA serials. |

Use subcommand `--help` for the full flag list. Configuration paths are explicit. `configure server|client` uses the same implementation as the corresponding `init` command. See the [CLI contract](docs/SPEC/01-scope-architecture.md#cli契約), [configuration command](docs/configure.md), and [examples for common commands](docs/quickstart.md#普段使うコマンド).

Release archives include Linux-only installation and removal scripts. They install only the executable and their management metadata; operational data and systemd configuration are intentionally preserved. See [Linux installation](docs/linux-installation.md).

## Configuration and authorization

Use `server.yaml` for the server workflow and `client.yaml` on each receiving host. In `server.yaml`, `pipeline.dns` groups the inventory, Sheets source, zone settings, and NSD deployment. `pipeline.certificates.renewal` configures issuance and renewal. After reviewing and applying the DNS candidate, run `cert renew` separately to publish due certificates. See the [configuration guide](docs/configure.md).

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

The server and client keep immutable version directories with atomically switched `current` links. The client verifies the artifact hashes and local policy before switching. An unchanged generation skips file changes and activation. Activation uses a locally configured systemd `reload` or `restart`, or a local executable with an explicit argument list. Both modes can verify the TLS endpoint's leaf fingerprint. Activation failure triggers rollback and repeats the configured operation when an older generation is available. Failed recovery and failed first activation leave a record for manual recovery.

Renewal uses Go's X.509 implementation and an existing RSA CA. It creates a new leaf key, validates the issued bundle, then publishes it. If the CA key uses supported encrypted PEM, OpenSSL decrypts it in memory using a protected file descriptor for the passphrase. No new root CA is generated. See [deployment](docs/SPEC/04-certificate-deployment.md) and [renewal](docs/certificate-renewal.md).

## NSD deployment

CertPouch generates forward and IPv4 reverse candidates from inventory and configured static records. The staging command runs `nsd-checkzone`. The apply command validates the candidate and rollback generations, checks the active serials, atomically switches the snapshot link, reloads affected zones, and confirms the new serials over loopback TCP. Failed activation restores the old records with newer serials when possible. Zone membership changes are configured separately in NSD. See [DNS and private PKI](docs/SPEC/05-dns-pki.md) and the [NSD guide](docs/dns-sync.md).

## Verification, releases, and branding

Local verification uses Go tests and vet, with race testing for shared state. Integration tests use generated test CAs and isolated services. Before migrating an existing deployment, verify the real inventory, NSD configuration, service permissions, CA protection, and backup recovery using the [acceptance guide](docs/release-and-acceptance.md).

GitHub Actions verifies the code. Release Please maintains a release PR, creates a tag and draft release after that PR is merged, and the workflow attaches verified Linux amd64 and arm64 archives and checksums. The draft is published manually.

Brand source assets, generated PNGs, and the MIT license are documented in the [brand specification](docs/SPEC/07-brand.md).
