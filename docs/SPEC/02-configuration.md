# 設定仕様

直接発行者が共通なら、`server.yaml` の `storage.issuer_certificate` と `client.yaml` の `validation.issuer_certificate` にそれぞれ一度だけ絶対パスを書く。証明書ごとの `issuer_certificate` があれば優先する。信頼チェーンの検証には引き続き `trust_ca_file` を使う。

`expected_dns_sans` と `expected_ip_sans` を両方省略すると、ドットを含む有効なDNSホスト名の `name` を唯一の期待DNS SANにする。別名やIP SANが必要な場合は期待するSAN集合を明記する。個別指定と既定値は合成しない。

標準のサーバー設定では `pipeline.dns.inventory`、`pipeline.dns.sheets`、`pipeline.dns.zones`、`pipeline.dns.deployment` に台帳からNSD反映までの設定をまとめる。発行条件は `pipeline.certificates.renewal`、配布ポリシーは `certificates` と `clients` に置く。階層化した設定と旧トップレベルのパイプライン設定は混在できない。

運用者が管理する設定YAMLはサーバー側の `server.yaml` と取得・配置側の `client.yaml` の2枚とする。`server.yaml` は配布サーバー、DNS台帳、Google Sheets取得、ゾーン生成、証明書更新の節をまとめる。資格情報、証明書、秘密鍵、失効リストは設定値から参照する保護対象データであり、設定YAMLには埋め込まない。

## 要件

YAMLを使用し、厳密に検証する。配布はHTTPSのみとし、適切なCAバンドルでTLSを検証する。資格情報を含む設定は0600、秘密鍵は0600を既定とする。例には文書用ドメイン・IPだけを使用する。

サーバーにはクライアントID、認証情報、無効化手段、証明書名ごとの接続元認可が必要である。DNS台帳を使用する場合は、証明書の論理名に対応するアドレスから認可を導出する。資格情報は平文ではなくハッシュで保存し、定数時間で比較する。

## 配布認証の設定

サーバーは `token_hash_file` のSHA-256ハッシュを読み、クライアントは `token_file` の平文トークンを読む。生成手順は[初回導入](../quickstart.md)を参照する。

`server.tls.client_ca` を設定するとmTLSが有効になる。各有効クライアントに `client_certificate_sha256` を指定する。トークン認証と接続元IP認可も併用する。[mTLSの設定](../mtls.md)

## サーバー設定

以下は静的割り当てモードの設定例。DNS台帳モードは後述する。

```yaml
server:
  listen: "0.0.0.0:8443"
  tls:
    certificate: "/etc/certpouch/server.crt"
    private_key: "/etc/certpouch/server.key"
storage:
  certificate_dir: "/var/lib/certpouch/certificates"
  trust_ca_file: "/etc/certpouch/issuers.pem"
certificates:
  - name: "web.example.internal"
    expected_dns_sans: ["web.example.internal"]
    expected_ip_sans: []
    issuer_certificate: "/etc/certpouch/intermediate.crt"
clients:
  - id: "web-server-01"
    enabled: true
    allowed_ips: ["192.0.2.10/32"]
    allowed_certificates: ["web.example.internal"]
    token_hash_file: "/etc/certpouch/credentials/web-server-01.sha256"
```

| 項目 | 検証 |
| --- | --- |
| `server.listen` | 有効なhost:port。HTTP併設やHTTPへのフォールバックは禁止 |
| `server.publication_refresh` | 任意。1秒以上の期間を指定すると公開ストアを定期的に再検証して配布在庫へ反映する |
| `server.inventory_refresh` | 任意。DNS台帳モードで1秒以上の期間を指定すると、キャッシュまたはCSVからIP認可を再計算する |
| `server.revocation_file` | 任意。失効させるクライアントIDとmTLS証明書のフィンガープリントを記録したYAMLへの絶対パス |
| `server.revocation_refresh` | 任意。失効リストを読み直す間隔。1秒以上で既定は5秒 |
| TLSファイル | PEMが読め、鍵が一致すること。配布対象の鍵と分離する |
| `certificate_dir` | 絶対パス。管理対象外への参照を許さない |
| `trust_ca_file` | 証明書公開時に使う信頼アンカー。OS既定に暗黙依存しない |
| `certificates` | 公開可能な論理名と検証ポリシー。重複禁止 |
| `issuer_certificate` | 期待する直接発行者を証明書で指定し、DN文字列だけで判定しない |
| `clients[].id` | 空・重複を禁止 |
| `enabled` | 省略時false。無効なクライアントの要求を拒否 |
| `allowed_ips` | 空リストは禁止。単一IPとCIDRを許容し、構文不正は起動失敗 |
| `allowed_certificates` | 明示列挙。ワイルドカード禁止。未登録名は設定エラー |
| `token_hash_file` | SHA-256の小文字16進64桁を1行で格納。形式不正・読み込み失敗は起動失敗 |

### DNS台帳モード

`pipeline.dns.inventory` を設定すると、サーバーは起動時にSheets取得結果のJSONキャッシュまたはCSVを読み取る。どちらか一方を指定する。

| 項目 | 役割 |
| --- | --- |
| `pipeline.dns.inventory.cache_file` | Sheetsの取得結果を保存したファイルの絶対パス |
| `pipeline.dns.inventory.csv_files` | キャッシュの代わりに使う、シートごとのCSVエクスポートの絶対パス一覧 |
| `pipeline.dns.inventory.max_age` | 任意。`dns fetch` の最終成功時刻から使える期間。キャッシュ用の設定で、配布サーバーでは `server.inventory_refresh` が必要 |
| `pipeline.dns.inventory.fallback_prefix` | 各範囲でB列が指定されるまで使うIPv4プレフィックス |
| `pipeline.dns.inventory.zone` | 台帳上の相対名を完全修飾名にするためのゾーン名 |
| `clients[].certificate_access_overrides` | 任意。公開対象の証明書名をキー、追加で取得を許可する台帳ホスト名の配列を値とする |

DNS台帳モードではクライアント設定の `allowed_ips` と `allowed_certificates` を省く。証明書の論理名と同名の台帳レコードを基本の取得元とし、例外設定のホストを証明書・クライアント単位で追加する。`certificates` のサーバー側定義で公開対象と検証方針を指定する。未登録証明書や台帳にない例外ホストは設定エラーになる。[実際の設定例と運用](../dns-inventory.md)

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
certificates:
  - name: "web.example.internal"
    destination: "/etc/certpouch/certificates/web.example.internal"
    expected_dns_sans: ["web.example.internal"]
    expected_ip_sans: []
    issuer_certificate: "/etc/ssl/certpouch/intermediate.crt"
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
- サービスユーザーが0600の鍵を読めない場合、専用グループと0640等を明示する。親ディレクトリのアクセス権も確認する。
- モードは文字列で受け取り、秘密鍵のother権限、group書き込み、実行ビットを禁止する。
- `destination`と`state_dir`は絶対パス。証明書間の配置先重複・包含関係は禁止する。
- 反映操作には証明書ごとに `service` または `command` を指定する。両者は同時に設定できない。`service.action` は `reload` または `restart` に限定する。
- unitはオプションと解釈される先頭ハイフン、空白、制御文字、パス区切りを拒否する。シェル展開しない。
- SAN期待値はローカル設定で保持し、サーバー応答だけを根拠に置き換えない。
- `command.args` は実行ファイル名を先頭とする非空の文字列配列とする。Pullはローカル設定から引数をそのまま渡して実行する。`command.timeout` は正の期間で、既定は30秒。
- `service.verify_tls` または `command.verify_tls` に接続先の `address` と `server_name` を指定すると、配置後に接続先のリーフ証明書を照合する。TLS照合の `timeout` は既定15秒で、1秒から2分を指定できる。

## 共通検証・更新

未知キー、重複キー、型不一致、空の必須値、重複名、不正なパスをエラーにする。論理名とIDはASCII英数字で始まる英数字・`.`・`_`・`-`の1〜128文字とし、`.`と`..`を禁止する。

Linuxでは設定・入力ファイルのgroup/other書き込みを拒否する。トークンや秘密鍵などの秘密情報ファイルではgroup/otherの権限を拒否する。実行ユーザーがファイルを読めるよう所有者や権限を設定する。資格情報をエラー本文へ埋め込まない。

YAMLや資格情報の変更は再起動で反映する。無効化・ローテーション時は設定検証後に再起動し、旧資格情報の拒否を確認する。台帳の定期読み込みを有効にした場合、IPルールだけを再計算する。台帳の読み込みや例外の再解決が失敗した場合は、成功まで台帳モードの認証を停止する。
