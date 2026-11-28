# 設定仕様

運用者が管理する設定YAMLは、サーバー側の `server.yaml` と取得・配置側の `client.yaml` の2枚とする。資格情報、証明書、秘密鍵、失効リストは設定値から参照する保護対象データであり、設定YAMLには埋め込まない。

## 要件

YAMLを使用し、厳密に検証する。配布はHTTPSのみとし、適切なCAバンドルでTLSを検証する。資格情報を含む設定は0600、秘密鍵は0600を既定とする。例には文書用ドメイン・IPだけを使用する。

サーバーにはクライアントID、認証情報、無効化手段、証明書名ごとの接続元認可が必要である。DNS台帳を使用する場合は、証明書の論理名に対応するアドレスから認可を導出する。資格情報は平文ではなくハッシュで保存し、定数時間で比較する。

## server.yaml（v2形式）

`server.yaml` は作業単位の4節だけで構成する。すべてのサーバー側コマンド（`server`、`pipeline`、`dns fetch|sync|diff|apply`、`cert publish|renew|status`）が同じファイルを読み、各コマンドは必要な節だけを検証する。全項目の例は[サーバー設定例](../../configs/server.example.yaml)を参照する。

| 節 | 役割 |
| --- | --- |
| `inventory` | 台帳の入力元。Sheets取得結果のキャッシュまたはCSV、IPv4の既定プレフィックス、取得間隔と期限、Sheetsの取得条件 |
| `dns` | 正引きゾーン名、SOA・NS・TTL・固定レコード、管理するNSDのパス |
| `certificates` | 既存CA、信頼CA、公開ストア、有効期間と更新時期、発行・配布する名前 |
| `distribution` | HTTPS配布APIの待ち受け、TLS、定期読み直し、失効リスト、クライアントと取得制限 |

CertPouch v1のトップレベルキー（`server`、`storage`、`dns_inventory`、`google_sheets`、`zones`、`renewal`、`pipeline`、`clients`）と、リスト形式のトップレベル `certificates` は受け付けない。これらを検出した場合は、移行先のv2キーを示して起動を拒否する。

### inventory

| 項目 | 検証・役割 |
| --- | --- |
| `cache_file` | Sheets取得結果を保存するJSONキャッシュの絶対パス。`csv_files` と排他 |
| `csv_files` | キャッシュの代わりに読む、シートごとのCSVエクスポートの絶対パス一覧。Sheets取得とは併用しない |
| `fallback_prefix` | 各範囲でB列が指定されるまで使うIPv4プレフィックス |
| `poll_interval` | 任意。`pipeline --watch` の取得間隔。1秒以上、既定3分 |
| `max_age` | 任意。`dns fetch` の最終成功時刻から台帳を使える期間。`cache_file` が必要 |
| `sheets.spreadsheet_id`, `sheets.ranges`, `sheets.credentials_file` | Sheets取得の対象と、閲覧専用サービスアカウントJSONの絶対パス |

`cache_file` と `csv_files` の両方を省略すると台帳を使わない。このとき配布は `allowed_ips` による静的割り当てになり、DNS生成と台帳からの発行は使えない。

### dns

| 項目 | 検証・役割 |
| --- | --- |
| `zone` | 正引きゾーン名。台帳上の相対名を完全修飾名にする |
| `zones` | SOA（`primary`、`mailbox`、`refresh`、`retry`、`expire`、`negative_ttl`）、`nameservers`、`ttl`、`reverse_zones`、`static_records`。`serial` と `previous_serials` は任意で、管理世代を持つ前の個別 `dns sync` だけで使う。通常は書かず、初回の候補は `dns sync --serial N`、以降は管理中の世代から引き継ぐ。serialの出所がない `dns sync --dry-run` はserial 1で表示する |
| `nsd.config_file`, `nsd.include_file` | NSDの設定と、CertPouch専用のinclude |
| `nsd.stage_dir`, `nsd.zone_dir` | 候補と管理世代を置く既存の絶対ディレクトリ |
| `nsd.dns_address` | SOA確認に使うループバックのアドレス。既定 `127.0.0.1:53` |

### certificates

| 項目 | 検証・役割 |
| --- | --- |
| `store_dir` | 公開ストアの絶対パス。管理対象外への参照を許さない |
| `trust_ca_file` | 発行・公開時にチェーンを検証する信頼アンカー。OS既定に暗黙依存しない |
| `issuer.certificate_file` | 期待する直接発行者のCA証明書。DN文字列だけで判定しない |
| `issuer.private_key_file`, `issuer.chain_file`, `issuer.passphrase_file` | 発行する場合だけ必要。署名するホスト以外には置かない。パスフレーズはファイルで渡す |
| `validity`, `renew_before` | 発行時の有効期間と更新開始時期。`renew_before` は `validity` より短い |
| `targets` | 任意。発行・配布する名前を固定する場合のリスト |

`targets` を省略して `issuer.certificate_file` を指定すると、台帳上のすべての名前と別名にそれぞれ独立した証明書を発行・配布する。このとき台帳（`inventory.cache_file` または `csv_files`）が必要である。`dns fetch` は使えず、`pipeline --once` または `--watch` がNSDでの反映を確認してからキャッシュを更新する。

`targets` を指定すると、列挙した名前だけを配布対象にする。各要素は `name`、任意の `expected_dns_sans`、`expected_ip_sans`、`issuer_certificate`（省略時は `issuer.certificate_file`）を持つ。`issuer.private_key_file` を指定すると、すべての対象を `cert renew` で発行・更新する。要素ごとの `validity` と `renew_before` は共通値より優先し、秘密鍵がない場合は指定できない。秘密鍵がない場合は `cert publish` で既存CAの発行済み証明書を登録する。

`expected_dns_sans` と `expected_ip_sans` を両方省略すると、ドットを含む有効なDNSホスト名の `name` を唯一の期待DNS SANにする。別名やIP SANが必要な場合は期待するSAN集合を明記する。個別指定と既定値は合成しない。名前は重複禁止。

### distribution

| 項目 | 検証・役割 |
| --- | --- |
| `listen` | 有効なhost:port。HTTP併設やHTTPへのフォールバックは禁止 |
| `tls.certificate_file`, `tls.private_key_file` | 配布APIのHTTPS証明書と鍵。PEMが読め、鍵が一致すること。配布対象の鍵と分離する |
| `tls.client_ca` | 任意。設定するとmTLSを要求する。各有効クライアントに `client_certificate_sha256` が必要。[mTLSの設定](../mtls.md) |
| `refresh_interval` | 公開ストアと台帳を読み直す間隔。1秒以上、既定5秒 |
| `revocation_file`, `revocation_refresh` | 任意。失効させるクライアントIDとmTLS証明書のフィンガープリントを記録したYAMLと、その読み直し間隔（1秒以上、既定5秒） |
| `clients[].id` | 空・重複を禁止 |
| `clients[].enabled` | 省略時false。無効なクライアントの要求を拒否 |
| `clients[].token_hash_file` | SHA-256の小文字16進64桁を1行で格納。形式不正・読み込み失敗は起動失敗 |
| `clients[].allowed_ips` | 台帳を使わない場合だけ指定する。単一IPとCIDRを許容し、構文不正は起動失敗 |
| `clients[].allowed_certificates` | 明示列挙。ワイルドカード禁止 |
| `clients[].inventory_access` | 台帳を使う場合の取得範囲。`ip` は台帳IP一致（既定）、`names` は名前の許可リスト、`ip_and_names` は両方、`all` は追加制限なし。`names` と `ip_and_names` には `allowed_certificates` が必須 |
| `clients[].certificate_access_overrides` | 任意。証明書名をキー、追加で取得を許可する台帳ホスト名の配列を値とする |

台帳を使う場合、証明書の論理名と同名の台帳レコードを基本の取得元とし、例外ホストを証明書・クライアント単位で追加できる。すべてのモードで認証が必要である。未登録証明書や台帳にない例外ホストは設定エラーになる。[実際の設定例と運用](../dns-inventory.md)

台帳を使わない静的割り当ての例を示す。

```yaml
certificates:
  store_dir: "/var/lib/certpouch/certificates"
  trust_ca_file: "/etc/certpouch/ca/roots.pem"
  issuer:
    certificate_file: "/etc/certpouch/ca/issuer.pem"
  targets:
    - name: "web.example.internal"
distribution:
  listen: "192.0.2.53:8443"
  tls:
    certificate_file: "/etc/certpouch/tls/server.pem"
    private_key_file: "/etc/certpouch/tls/server.key"
  clients:
    - id: "web-server-01"
      enabled: true
      allowed_ips: ["192.0.2.10/32"]
      allowed_certificates: ["web.example.internal"]
      token_hash_file: "/etc/certpouch/tokens/web-server-01.sha256"
```

## 配布認証の設定

サーバーは `token_hash_file` のSHA-256ハッシュを読み、クライアントは `token_file` の平文トークンを読む。生成手順は[初回導入](../quickstart.md)を参照する。

トークンは暗号学的乱数32バイト以上をbase64url（paddingなし）で表現する。その文字列のSHA-256を保存する。人が選んだパスワードを高速ハッシュで保護する方式として使用しない。ファイル末尾の単一改行だけを除去し、埋め込み改行や空白は拒否する。

## クライアント設定

```yaml
server:
  url: "https://cert-server.example.internal:8443"
  ca_file: "/etc/ssl/certpouch/server-ca.crt"
client:
  id: "web-server-01"
  token_file: "/etc/certpouch/client.token"
  state_dir: "/var/lib/certpouch/client"
validation:
  trust_ca_file: "/etc/ssl/certpouch/issuers.pem"
  issuer_certificate: "/etc/ssl/certpouch/intermediate.crt"
certificates:
  - name: "web.example.internal"
    destination: "/etc/certpouch/certificates/web.example.internal"
    expected_dns_sans: ["web.example.internal"]
    expected_ip_sans: []
    owner: "root"
    group: "root"
    certificate_mode: "0644"
    private_key_mode: "0600"
    service:
      unit: "nginx.service"
      action: "reload"
```

- `server.url`はHTTPSのoriginのみとし、userinfo、query、fragment、追加パスは禁止する。
- 通信TLS用CAと配布証明書検証用CAは別の信頼設定として扱う。
- 直接発行者が共通なら `validation.issuer_certificate` に一度だけ書く。証明書ごとの `issuer_certificate` があれば優先する。
- サービスユーザーが0600の鍵を読めない場合、専用グループと0640等を明示する。親ディレクトリのアクセス権も確認する。
- モードは文字列で受け取り、秘密鍵のother権限、group書き込み、実行ビットを禁止する。
- `destination`と`state_dir`は絶対パス。証明書間の配置先重複・包含関係は禁止する。
- 配置後の操作は任意。証明書ごとに `service` または `command` を指定できる。両方省略した場合は配置だけを行う。両者は同時に指定できない。`service.action` は `reload` または `restart` に限定する。
- unitはオプションと解釈される先頭ハイフン、空白、制御文字、パス区切りを拒否する。シェル展開しない。
- SAN期待値はローカル設定で保持し、サーバー応答だけを根拠に置き換えない。
- `command.args` は実行ファイル名を先頭とする非空の文字列配列とする。Pullはローカル設定から引数をそのまま渡して実行する。`command.timeout` は正の期間で、既定は30秒。
- `service.verify_tls` または `command.verify_tls` に接続先の `address` と `server_name` を指定すると、配置後に接続先のリーフ証明書を照合する。TLS照合の `timeout` は既定15秒で、1秒から2分を指定できる。

## 共通検証・更新

未知キー、重複キー、型不一致、空の必須値、重複名、不正なパスをエラーにする。YAMLのエラーは行・列と、未知キー・重複キーの名前だけを示し、設定値は表示しない。設定値の検証エラーはv2のキー名（例 `distribution.tls.private_key_file`）で問題の項目を示す。論理名とIDはASCII英数字で始まる英数字・`.`・`_`・`-`の1〜128文字とし、`.`と`..`を禁止する。

Linuxでは設定・入力ファイルのgroup/other書き込みを拒否する。トークンや秘密鍵などの秘密情報ファイルではgroup/otherの権限を拒否する。実行ユーザーがファイルを読めるよう所有者や権限を設定する。資格情報をエラー本文へ埋め込まない。

YAMLや資格情報の変更は再起動で反映する。無効化・ローテーション時は設定検証後に再起動し、旧資格情報の拒否を確認する。台帳の定期読み込みは起動時の設定から認可ルールを再計算する。台帳から発行対象を決める構成では、同じ台帳から公開ポリシーも再計算し、台帳から消えた名前に対応する例外許可を外す。台帳の読み込みや認可ルールの再計算が失敗した場合は、成功まで台帳モードの認証を停止する。
