# CertPouch specification

CertPouch is a single Go binary for self-hosted private DNS, private PKI, and HTTPS pull-based certificate distribution. It uses an existing private CA and activates certificates and DNS on Linux. The license is MIT.

How to read this document:

- [Target scope](#target-scope) defines the intended workflow.
- The remaining sections and the [detailed specifications](docs/SPEC/README.md) describe the current implementation.
- [First deployment (Japanese)](docs/quickstart.md) walks through automated operation from Sheets and manual deployment of a single certificate.

## Target scope

An operator registers a hostname, an IP address, and optional aliases in Google Sheets. That single entry is the routine input for both DNS and certificate issuance. Adding a host requires no per-host YAML change and no issuance flag.

1. **Poll Sheets** at a configurable interval, aiming to reflect changes within a few minutes. Respect API quotas and back off on quota errors and transient failures. A future webhook may trigger the same synchronization sooner.
2. **Prepare NSD zones at startup** from the configured IP ranges. Range changes take effect when CertPouch restarts. Regular synchronization updates records inside the prepared zones and confirms DNS activation before it reconciles certificate issuance, renewal, and availability.
3. **Issue one certificate per name.** Use an existing CA and a shared issuance policy. Each hostname and each alias gets its own certificate and private key. Authorized receiving servers pull the names they need over HTTPS. Local placement can trigger a configured service action or command.
4. **Stop serving removed names** after DNS removal is confirmed: no new retrieval and no automatic renewal. Certificates already deployed may stay in service until they expire. Immediate revocation, for example after a key compromise, is a separate operation.

If certificate issuance fails after DNS activation succeeded, keep the active DNS records. Retry issuance for that name on the next scheduled synchronization, even if the DNS records did not change.

Authorization rules:

- Inventory membership decides which names are issued.
- Retrieval defaults to token authentication. Operators can explicitly select mTLS-only or direct source IP authentication.
- Each client can be restricted by inventory source IP match (the default), a certificate name allowlist, both, or explicitly neither.
- A valid inventory change is never blocked by a threshold on removed or replaced records.

See the [target workflow specification](docs/SPEC/08-inventory-workflow.md).

### Current implementation gap

- `pipeline --watch` provides periodic synchronization. Startup zone registration is available through a dedicated NSD include.
- The [server configuration example](configs/server.example.yaml) shows the v2 layout.
- The inventory cache is updated after DNS confirmation. Distribution reflects the change on its configured refresh interval.

## Components and data flow

| Component | Role |
| --- | --- |
| Inventory | Google Sheets, an existing Sheets JSON cache, or CSV exports. CertPouch can fetch the Sheets cache with a read-only service account. A cache or CSV works locally without Google access. |
| DNS | The inventory drives NSD zone generation. In DNS inventory mode it also drives source IP authorization for distribution. |
| PKI | Administrators publish an existing certificate or issue one with a configured RSA CA. The signing key stays with the issuance process. |
| Distribution server | Loads validated generations from a local store and serves them over HTTPS. |
| Receiving host | Retrieves only authorized certificates, validates the downloaded generation, switches its local `current` link, and runs the systemd action or command configured on that host. |

The CLI lives in `cmd/certpouch`. Internal packages cover YAML configuration, the HTTPS server and distribution, the pull client, PKI, DNS, Sheets access, storage, and deployment. The Go version and module dependencies are in [go.mod](go.mod). See the [architecture diagram](docs/SPEC/01-scope-architecture.md#全体構成).

## Command reference

| Command | Behavior |
| --- | --- |
| `install.sh` / `uninstall.sh` | Installs or removes the Linux binary while preserving configuration, credentials, certificates, state, and systemd units. |
| `certpouch update [--check]` | Checks the latest stable GitHub Release and atomically replaces a standalone Linux executable after verification. |
| `certpouch configure dns` | Creates validated DNS and optional Sheets-fetch configuration from flags or prompts. Never overwrites existing files. |
| `certpouch configure client` | Creates a client YAML, random token, SHA-256 token hash, and deployment directory under a new output directory on Linux. |
| `certpouch configure server` | Creates a server YAML and publication store from the client hash, TLS files, and existing DNS inventory. |
| `certpouch server --config PATH` | Loads validated publications and serves the HTTPS API. `--check-config` validates without opening the listener. |
| `certpouch pull --config PATH` | Compares authorized inventory with local state, then downloads, validates, and deploys updates. `--dry-run` previews without fetching private keys or changing anything. |
| `certpouch status --config PATH` | Reports local deployment and synchronization state without contacting the server. |
| `certpouch cert publish --config PATH` | Registers a validated leaf certificate, private key, and optional intermediate chain. |
| `certpouch cert renew --config PATH` | Issues missing certificates and renews expiring ones using the `certificates` section. Supports `--dry-run`. |
| `certpouch cert status --config PATH` | Checks published leaf certificates and their expiry. |
| `certpouch dns fetch --config PATH` | Fetches and validates a Sheets cache. `--status` reads the local cache state. |
| `certpouch pipeline --config PATH` | Refreshes the Sheets cache and prints the path of a validated DNS candidate. |
| `certpouch pipeline --config PATH --apply CANDIDATE` | Applies the selected candidate to an initialized NSD deployment. |
| `certpouch dns sync --config PATH` | Renders a preview with `--dry-run`, or creates an NSD-validated candidate with `--stage-dir`. |
| `certpouch dns diff --candidate PATH` | Compares a candidate with a managed `--zone-dir` or a staged `--baseline`. |
| `certpouch dns apply --candidate PATH --zone-dir PATH` | Initializes the managed snapshot with `--initialize`, or updates NSD and verifies the served SOA serials. |

- Run `certpouch help <command>` or `--help` for the full flag list. Requested help goes to stdout.
- Configuration paths are always explicit.
- See the [CLI contract](docs/SPEC/01-scope-architecture.md#cli契約), the [configuration command](docs/configure.md), and [common command examples (Japanese)](docs/manual-distribution.md#普段使うコマンド).

Release archives include Linux-only installation and removal scripts. They install only the executable and its management metadata, and they intentionally leave operational data and systemd configuration alone. See [Linux installation](docs/linux-installation.md).

## Configuration and authorization

Use `server.yaml` on the server and `client.yaml` on each receiving host. `server.yaml` has four sections:

| Section | Contents |
| --- | --- |
| `inventory` | Cache or CSV files, and the Sheets source |
| `dns` | Zones and the managed NSD |
| `certificates` | The existing CA, the store, and the issued names |
| `distribution` | The HTTPS API and its clients |

- Without `certificates.targets`, every inventory name is issued. With targets, only the listed names are distributed.
- CertPouch v1 top-level keys such as `server`, `storage`, `dns_inventory`, and `pipeline` are rejected, and the error names the v2 key to use.
- The YAML loaders reject unknown and duplicate keys.
- Only the optional runtime revocation list lives outside these two files.

See the [configuration guide](docs/configure.md), the [server YAML example](configs/server.example.yaml), and the [client YAML example](configs/client.example.yaml).

### Authentication and authorization

- `distribution.auth` sets the default authentication mode and `distribution.clients[].auth` can override it. The receiving host selects the matching `client.auth`. Modes are `token` (default), `mtls`, and `ip`.
- In token mode the receiving host holds the token and reads it from `client.token_file`.
- The server reads only its SHA-256 digest from `distribution.clients[].token_hash_file` and compares it in constant time.
- In DNS inventory mode, the server checks the TCP source IP against the certificate's logical name and any per-client certificate exceptions.
- Static mode uses IP/CIDR allowlists and certificate assignments.
- Forwarding headers are ignored.
- With a TLS client CA, all connections require a verified client certificate. Token mode also checks a bearer token, while mTLS mode checks the certificate fingerprint alone for identity.
- IP mode trusts the direct source address. It requires inventory IP matching or static assignments with exact IP addresses and explicit certificate names. Use it only on networks where source addresses are trusted.
- In DNS inventory mode with `distribution.auth: ip`, hosts that are not listed in `distribution.clients` can fetch the names whose inventory address matches their source IP. Requests without a client ID or with an unregistered ID use this rule; registered IDs keep their own settings, and disabled IDs are rejected.
- The runtime revocation list can deny a client ID or an mTLS fingerprint.

Use documentation-only addresses and domains in examples. Restrict access to configuration, credentials, and signing keys. See [configuration](docs/SPEC/02-configuration.md), [DNS authorization](docs/dns-inventory.md), [mTLS](docs/mtls.md), and [revocation](docs/client-revocation.md).

## HTTPS distribution API

| Endpoint | Purpose |
| --- | --- |
| `GET /api/v1/health` | Reports server health, including publication and inventory refresh failures and configured inventory expiry. |
| `GET /api/v1/certificates` | Lists authorized current certificates and their metadata. |
| `GET /api/v1/certificates/{name}/metadata` | Returns metadata for a certificate's current generation. |
| `GET /api/v1/certificates/{name}/download?version={version}` | Returns the requested generation and its artifact hashes. |

- TLS 1.2 or later is required. The client verifies the server name against the configured TLS CA.
- Requests carry a client ID. Only token mode sends a bearer token.
- Responses are JSON. The leaf, chain, and private key are separate artifacts.
- Logs record distribution outcomes without secrets.

See the [API contract](docs/SPEC/03-distribution-api.md).

## Certificate validation, deployment, and renewal

**Validation.** Before publication and before deployment, CertPouch checks the leaf and key pair, the validity period, the DNS/IP SAN set, the direct issuer, the intermediate chain, and an explicit Server Authentication EKU. Distributed keys are RSA, at least 2048 bits, in unencrypted PKCS#1 or PKCS#8 PEM. The CA private key is never part of a distributed bundle.

**Deployment.**

- The server and client keep immutable version directories and switch a `current` link atomically.
- The client verifies the artifact hashes and its local policy before switching. An unchanged generation causes no file change and no activation.
- Activation is a locally configured systemd `reload` or `restart`, or a local executable with an explicit argument list. Either can verify the leaf fingerprint served by the TLS endpoint.
- If activation fails and an older generation exists, the client rolls back and repeats the configured operation. Failed recovery and failed first activation leave a record for manual recovery.

**Renewal.** Renewal uses Go's X.509 implementation and an existing RSA CA. It creates a new leaf key, validates the issued bundle, and then publishes it. For a supported encrypted PEM CA key, OpenSSL decrypts it in memory and reads the passphrase from a protected file descriptor. No new root CA is ever generated.

See [deployment](docs/SPEC/04-certificate-deployment.md) and [renewal](docs/certificate-renewal.md).

## NSD deployment

1. CertPouch generates forward and IPv4 reverse candidates from the inventory and configured static records.
2. The staging command runs `nsd-checkzone`.
3. The apply command validates the candidate and rollback generations, checks the active serials, switches the snapshot link atomically, reloads the affected zones, and confirms the new serials over loopback TCP.
4. If activation fails, it restores the old records with newer serials when possible.

Zone membership changes are configured separately in NSD. See [DNS and private PKI](docs/SPEC/05-dns-pki.md) and the [NSD guide](docs/dns-sync.md).

## Verification, releases, and branding

- **Local verification** uses Go tests and vet, plus race testing for shared state. Integration tests use generated test CAs and isolated services.
- **Before migrating** an existing deployment, verify the real inventory, NSD configuration, service permissions, CA protection, and backup recovery with the [acceptance guide](docs/release-and-acceptance.md).
- **Releases:** GitHub Actions verifies the code. Release Please maintains a release PR and creates a tag and a draft release once that PR is merged. The workflow verifies and attaches the Linux amd64 and arm64 archives and checksums, then publishes the stable release. Manual rebuilds of existing drafts leave their publication state unchanged.
- **Branding:** brand source assets, generated PNGs, and the MIT license are described in the [brand specification](docs/SPEC/07-brand.md).
