# 設定仕様

運用者が管理する設定YAMLは次の2つとする。

| ファイル | 置く場所 | 役割 |
| --- | --- | --- |
| `server.yaml` | サーバー | 台帳、DNS、証明書の発行・公開、HTTPS配布 |
| `client.yaml` | 取得ホスト | 取得先、取得対象、配置先、配置後の操作 |

資格情報、証明書、秘密鍵、失効リストは設定から参照する保護対象データであり、YAMLには埋め込まない。

## 要件

- YAMLを使用し、厳密に検証する。
- 配布はHTTPSのみとし、適切なCAバンドルでTLSを検証する。
- 資格情報を含む設定と秘密鍵は、0600を既定とする。
- 例には文書用のドメイン・IPだけを使用する。
- サーバーには、クライアントID、認証情報、無効化手段、証明書名ごとの接続元認可が必要である。
- DNS台帳を使用する場合は、証明書の論理名に対応するアドレスから認可を導出する。
- 資格情報は平文ではなくハッシュで保存し、定数時間で比較する。

## server.yaml（v2形式）

`server.yaml` は作業単位の4節だけで構成する。すべてのサーバー側コマンド（`server`、`pipeline`、`dns fetch|sync|diff|apply`、`cert publish|renew|status`）が同じファイルを読み、各コマンドは必要な節だけを検証する。全項目の例は[サーバー設定例](../../configs/server.example.yaml)を参照する。

| 節 | 役割 |
| --- | --- |
| `inventory` | 台帳の入力元。Sheets取得結果のキャッシュまたはCSV、IPv4の既定プレフィックス、取得間隔と期限、Sheetsの取得条件 |
| `dns` | 正引きゾーン名、SOA・NS・TTL・固定レコード、管理するNSDのパス |
| `certificates` | 既存CA、信頼CA、公開ストア、有効期間と更新時期、発行・配布する名前 |
| `distribution` | HTTPS配布APIの待ち受け、TLS、定期読み直し、失効リスト、クライアントと取得制限 |

次の形式は受け付けない。検出した場合は、移行先のv2キーを示して起動を拒否する。

- CertPouch v1のトップレベルキー: `server`、`storage`、`dns_inventory`、`google_sheets`、`zones`、`renewal`、`pipeline`、`clients`
- リスト形式のトップレベル `certificates`

### inventory

| 項目 | 検証・役割 |
| --- | --- |
| `cache_file` | Sheets取得結果を保存するJSONキャッシュの絶対パス。`csv_files` と排他 |
| `csv_files` | キャッシュの代わりに読む、シートごとのCSVエクスポートの絶対パス一覧。Sheets取得とは併用しない |
| `fallback_prefix` | 各範囲でB列が指定されるまで使うIPv4プレフィックス |
| `poll_interval` | 任意。`pipeline --watch` の取得間隔。1秒以上、既定3分 |
| `max_age` | 任意。`dns fetch` の最終成功時刻から台帳を使える期間。`cache_file` が必要 |
| `sheets.spreadsheet_id`、`sheets.ranges`、`sheets.credentials_file` | Sheets取得の対象と、閲覧専用サービスアカウントJSONの絶対パス |

`cache_file` と `csv_files` の両方を省略すると台帳を使わない。このとき配布は `allowed_ips` による静的割り当てになり、DNS生成と台帳からの発行は使えない。

### dns

| 項目 | 検証・役割 |
| --- | --- |
| `zone` | 正引きゾーン名。台帳上の相対名を完全修飾名にする |
| `zones` | SOA（`primary`、`mailbox`、`refresh`、`retry`、`expire`、`negative_ttl`）、`nameservers`、`ttl`、`reverse_zones`、`static_records` |
| `zones.serial`、`zones.previous_serials` | 任意。管理世代を持つ前に、個別の `dns sync` で使う場合だけ書く（下記） |
| `nsd.config_file`、`nsd.include_file` | NSDの設定と、CertPouch専用のinclude |
| `nsd.stage_dir`、`nsd.zone_dir` | 候補と管理世代を置く既存の絶対ディレクトリ |
| `nsd.dns_address` | SOA確認に使うループバックのアドレス。既定 `127.0.0.1:53` |

serialは通常、設定に書かない。

- 初回の候補は `dns sync --serial N` で指定する。
- 以降は管理中の世代から引き継ぐ。
- serialの出所がない `dns sync --dry-run` はserial 1で表示する。

### certificates

| 項目 | 検証・役割 |
| --- | --- |
| `store_dir` | 公開ストアの絶対パス。管理対象外への参照を許さない |
| `trust_ca_file` | 発行・公開時にチェーンを検証する信頼アンカー。OS既定に暗黙依存しない |
| `issuer.certificate_file` | 期待する直接発行者のCA証明書。DN文字列だけで判定しない |
| `issuer.private_key_file`、`issuer.chain_file`、`issuer.passphrase_file` | 発行する場合だけ必要。署名するホスト以外には置かない。パスフレーズはファイルで渡す |
| `validity`、`renew_before` | 発行時の有効期間と更新開始時期。`renew_before` は `validity` より短い |
| `targets` | 任意。発行・配布する名前を固定する場合のリスト |

発行・配布する名前は `targets` の有無で決まる。

| 構成 | 対象 | 必要な設定と制約 |
| --- | --- | --- |
| `targets` を省略し、`issuer.certificate_file` を指定 | 台帳上のすべての名前と別名。それぞれ独立した証明書を発行・配布する | 台帳（`inventory.cache_file` または `csv_files`）が必要。`dns fetch` は使えず、`pipeline --once` または `--watch` がNSDでの反映を確認してからキャッシュを更新する |
| `targets` を指定し、`issuer.private_key_file` も指定 | 列挙した名前。すべてを `cert renew` で発行・更新する | 要素ごとの `validity` と `renew_before` は共通値より優先する |
| `targets` を指定し、秘密鍵なし | 列挙した名前。`cert publish` で既存CAの発行済み証明書を登録する | 要素ごとの `validity` と `renew_before` は指定できない |

`targets` の各要素は `name`、任意の `expected_dns_sans`、`expected_ip_sans`、`issuer_certificate`（省略時は `issuer.certificate_file`）を持つ。名前の重複は禁止する。

期待SANの決まり方:

- `expected_dns_sans` と `expected_ip_sans` を両方省略すると、`name`（ドットを含む有効なDNSホスト名）を唯一の期待DNS SANにする。
- 別名やIP SANが必要な場合は、期待するSAN集合を明記する。
- 個別指定と既定値は合成しない。

### distribution

| 項目 | 検証・役割 |
| --- | --- |
| `auth` | 全クライアントの既定の認証方式。省略時は `token` |
| `listen` | 有効なhost:port。HTTPの併設やHTTPへのフォールバックは禁止 |
| `tls.certificate_file`、`tls.private_key_file` | 配布APIのHTTPS証明書と鍵。PEMが読め、鍵が一致すること。配布対象の鍵とは分離する |
| `tls.client_ca` | 任意。設定するとmTLSを要求する。各有効クライアントに `client_certificate_sha256` が必要。[mTLSの設定](../mtls.md) |
| `refresh_interval` | 公開ストアと台帳を読み直す間隔。1秒以上、既定5秒 |
| `revocation_file`、`revocation_refresh` | 任意。失効させるクライアントIDとmTLS証明書のフィンガープリントを記録したYAMLと、その読み直し間隔（1秒以上、既定5秒） |
| `clients[].id` | 空・重複を禁止 |
| `clients[].auth` | クライアント単位の上書き。省略時は `distribution.auth` を使う |
| `clients[].enabled` | 省略時false。無効なクライアントの要求を拒否 |
| `clients[].token_hash_file` | `token` で必須。他の方式では指定不可。SHA-256の小文字16進64桁を1行で格納 |
| `clients[].allowed_ips` | 台帳を使わない場合だけ指定する。単一IPとCIDRを許容し、構文不正は起動失敗 |
| `clients[].allowed_certificates` | 明示列挙。ワイルドカード禁止 |
| `clients[].inventory_access` | 台帳を使う場合の取得範囲（下表） |
| `clients[].certificate_access_overrides` | 任意。証明書名をキー、追加で取得を許可する台帳ホスト名の配列を値とする |

`inventory_access` の値:

| 値 | 取得できる範囲 |
| --- | --- |
| `ip`（既定） | 台帳IPが接続元IPと一致する名前 |
| `names` | `allowed_certificates` に列挙した名前 |
| `ip_and_names` | 両方の条件を満たす名前 |
| `all` | 追加制限なし |

`names` と `ip_and_names` には `allowed_certificates` が必須である。

台帳を使う場合の認可:

- 証明書の論理名と同名の台帳レコードを、基本の取得元とする。
- 例外ホストを証明書・クライアント単位で追加できる。
- `auth: ip` では `inventory_access: ip` または `ip_and_names` が必要である。
- `certificates.targets` で発行・配布する名前を固定する構成では、例外設定の証明書名は `targets` に、例外ホストは台帳に存在する必要がある。
- 台帳から発行対象を決める構成では、台帳に存在する証明書名と例外ホストの組み合わせだけを有効にする。台帳から消えた証明書名や例外ホストへの例外許可は次の読み直しで外し、ほかの有効な許可を維持する。

[実際の設定例と運用](../dns-inventory.md)

台帳を使わない静的割り当ての例:

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

| `auth` | サーバーの条件 | クライアントの条件 |
| --- | --- | --- |
| `token`（既定） | `token_hash_file` が必須。TLSクライアントCAの設定時はフィンガープリント照合も行う | `token_file` が必須。サーバーがmTLSを要求する場合はクライアント証明書と鍵も必要 |
| `mtls` | TLSクライアントCAとクライアント証明書のフィンガープリントが必須 | クライアント証明書と鍵が必須 |
| `ip` | 台帳IP一致、または静的設定で単一IP（`/32`・`/128` を含む）と明示した証明書名の両方が必須。TLSクライアントCAとの併用不可 | クライアントIDを送る。サーバー証明書は従来どおりCAとホスト名で検証する |

IP方式では、同じ接続元IPを共有する端末を区別できない。IPを信頼できる閉域ネットワークで使用する。すべての方式で、有効なクライアントIDと証明書ごとの取得制限が必要である。設定例と切り替え手順は[配布クライアントと認証方式](../distribution-clients.md)に示す。

- サーバーは `token_hash_file` のSHA-256ハッシュを読む。
- クライアントは `token_file` の平文トークンを読む。
- トークンは暗号学的乱数32バイト以上を、base64url（paddingなし）で表現する。その文字列のSHA-256を保存する。
- 人が選んだパスワードを高速ハッシュで保護する方式としては使用しない。
- ファイル末尾の単一改行だけを除去する。埋め込み改行や空白は拒否する。

生成手順は[手動配布の手順3](../manual-distribution.md#3-手動でクライアント資格情報を作る)を参照する。

## クライアント設定

```yaml
server:
  url: "https://cert-server.example.internal:8443"
  ca_file: "/etc/ssl/certpouch/server-ca.crt"
client:
  id: "web-server-01"
  auth: token
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

**接続先と検証**

- `server.url` はHTTPSのoriginのみとする。userinfo、query、fragment、追加パスは禁止する。
- 通信TLS用のCA（`server.ca_file`）と、配布証明書の検証用CA（`validation`）は別の信頼設定として扱う。
- 直接発行者が共通なら `validation.issuer_certificate` に一度だけ書く。証明書ごとの `issuer_certificate` があればそちらを優先する。
- SANの期待値はローカル設定で保持し、サーバー応答だけを根拠に置き換えない。
- mTLSを使う場合は `client.tls_certificate` と `client.tls_private_key` を両方指定する。

**配置先と権限**

- `destination` と `state_dir` は絶対パスとする。証明書間で配置先の重複・包含関係は禁止する。
- モードは文字列で受け取る。秘密鍵のother権限、group書き込み、実行ビットを禁止する。
- サービスユーザーが0600の鍵を読めない場合は、専用グループと0640などを明示する。親ディレクトリのアクセス権も確認する。

**配置後の操作**

- 配置後の操作は任意。証明書ごとに `service` または `command` のどちらかを指定できる。両方省略すると配置だけを行う。両方は同時に指定できない。
- `service.action` は `reload` または `restart` に限定する。
- `service.unit` は、オプションと解釈される先頭ハイフン、空白、制御文字、パス区切りを拒否する。シェル展開しない。
- `command.args` は実行ファイル名を先頭とする非空の文字列配列とする。Pullはローカル設定の引数をそのまま渡して実行する。
- `command.timeout` は正の期間で、既定は30秒。
- `service.verify_tls` または `command.verify_tls` に接続先の `address` と `server_name` を指定すると、配置後に接続先のリーフ証明書を照合する。TLS照合の `timeout` は既定15秒で、1秒から2分を指定できる。

## 共通検証・更新

**YAMLの検証**

- 未知キー、重複キー、型不一致、空の必須値、重複名、不正なパスをエラーにする。
- YAMLのエラーは行・列と、未知キー・重複キーの名前だけを示し、設定値は表示しない。
- 設定値の検証エラーは、v2のキー名（例 `distribution.tls.private_key_file`）で問題の項目を示す。
- 論理名とIDは、ASCII英数字で始まる英数字・`.`・`_`・`-` の1〜128文字とする。`.` と `..` は禁止する。

**ファイル権限**

- Linuxでは、設定・入力ファイルのgroup/other書き込みを拒否する。
- トークンや秘密鍵などの秘密情報ファイルでは、group/otherの権限を拒否する。
- 実行ユーザーがファイルを読めるよう、所有者や権限を設定する。
- 資格情報をエラー本文へ埋め込まない。

**変更の反映**

- YAMLや資格情報の変更は再起動で反映する。無効化・ローテーション時は設定を検証してから再起動し、旧資格情報が拒否されることを確認する。
- 台帳の定期読み込みでは、起動時の設定から認可ルールを再計算する。
- 台帳から発行対象を決める構成では、同じ台帳から公開ポリシーも再計算し、台帳から消えた名前に対応する例外許可を外す。
- 台帳の読み込みや認可ルールの再計算に失敗した場合は、成功するまで台帳モードの認証を停止する。
