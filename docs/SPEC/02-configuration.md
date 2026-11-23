# 設定仕様

## 要件

YAMLを使用し、厳密に検証する。配布はHTTPSのみとし、適切なCAバンドルでTLSを検証する。資格情報を含む設定は0600、秘密鍵は0600を既定とする。例には文書用ドメイン・IPだけを使用する。

サーバーにはクライアントID、認証情報、無効化手段、証明書名ごとの接続元認可が必要である。DNS台帳を使用する場合は、証明書の論理名に対応するアドレスから認可を導出する。資格情報は平文ではなくハッシュで保存し、定数時間で比較する。

## 原仕様の不整合

原仕様のサーバー例は`token_file`を示すが、本文は資格情報ハッシュの保存を要求する。本書ではサーバー用を`token_hash_file`、クライアント用を`token_file`とする案を採用する。原仕様の例をそのまま実装契約としない。

`server.tls.client_ca`は将来のmTLS用であり、Phase 1で設定するだけでクライアント認証が有効になると解釈しない。

## 詳細化案：サーバー設定

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

`dns_inventory` を設定すると、サーバーは起動時に既存のゾーン生成処理がGoogle Sheetsから保存したJSONを読み取る。

| 項目 | 役割 |
| --- | --- |
| `dns_inventory.cache_file` | Sheetsの取得結果を保存したファイルの絶対パス |
| `dns_inventory.csv_files` | キャッシュの代わりに使う、シートごとのCSVエクスポートの絶対パス一覧 |
| `dns_inventory.fallback_prefix` | 各範囲でB列が指定されるまで使うIPv4プレフィックス |
| `dns_inventory.zone` | 台帳上の相対名を完全修飾名にするためのゾーン名 |
| `clients[].certificate_access_overrides` | 任意。公開対象の証明書名をキー、追加で取得を許可する台帳ホスト名の配列を値とする |

DNS台帳モードではクライアント設定の `allowed_ips` と `allowed_certificates` を省く。証明書の論理名と同名の台帳レコードを基本の取得元とし、例外設定のホストを証明書・クライアント単位で追加する。`certificates` のサーバー側定義で公開対象と検証方針を指定する。未登録証明書や台帳にない例外ホストは設定エラーになる。[実際の設定例と運用](../dns-inventory.md)

トークンは暗号学的乱数32バイト以上をbase64url（paddingなし）で表現する案とする。その文字列のSHA-256を保存する。人が選んだパスワードを高速ハッシュで保護する方式として使用しない。ファイル末尾の単一改行だけを除去し、埋め込み改行や空白は拒否する。

## 詳細化案：クライアント設定

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
- サービスはローカル設定に記載されたsystemd unitのみ許可し、actionは`reload`または`restart`に限定する。
- unitはオプションと解釈される先頭ハイフン、空白、制御文字、パス区切りを拒否する。シェル展開しない。
- SAN期待値はローカル設定で保持し、サーバー応答だけを根拠に置き換えない。

## 詳細化案：共通検証・更新

未知キー、重複キー、型不一致、空の必須値、重複名、不正なパスをエラーにする。論理名とIDはASCII英数字で始まる英数字・`.`・`_`・`-`の1〜128文字とし、`.`と`..`を禁止する。

設定・トークン・鍵ファイルは起動ユーザーの所有とし、group/other書き込みを禁止する。秘密情報ファイルは所有者以外の読み取りも禁止する。資格情報をエラー本文へ埋め込まない。

Phase 1は設定のホットリロードを行わず、再起動で反映する。無効化・ローテーション時は設定検証後に再起動し、旧資格情報の拒否を確認する。稼働中要求への反映境界も[API仕様](03-distribution-api.md)に従う。
