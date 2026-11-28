# 配布クライアントと認証方式

`server.yaml` の `distribution.clients` に取得を許すクライアントを登録し、`distribution.auth` で認証方式を選ぶ手順です。どの方式でも、配布APIはHTTPSだけで提供し、クライアントは配布サーバーの証明書を `server.ca_file` で検証します。

## 認証方式を選ぶ

| `auth` | クライアントが示すもの | サーバーに置くもの | 向いている構成 |
| --- | --- | --- | --- |
| `token`（既定） | クライアントIDとBearerトークン | トークンのSHA-256（`token_hash_file`） | 通常の構成 |
| `mtls` | クライアントIDとクライアント証明書 | クライアントCA（`tls.client_ca`）と証明書のフィンガープリント | トークンファイルを配りたくない構成。[mTLSの設定](mtls.md) |
| `ip` | クライアントIDだけ | なし。接続元IPで判定する | 閉域ネットワークで、資格情報の管理を省きたい構成 |

- `distribution.auth` は全クライアントの既定値です。`distribution.clients[].auth` で、クライアントごとに上書きできます。
- `ip` は `tls.client_ca` と併用できません。`mtls` には `tls.client_ca` が必要です。`token` と `ip`、`token` と `mtls` は同じサーバーで混在できます。
- `token` で `tls.client_ca` を設定した場合は、トークンとクライアント証明書の両方を照合します。

> [!WARNING]
> `ip` 方式では、接続元IPを偽れる端末や、同じIPを共有する端末（NAT配下、同じホスト上の別ユーザーなど）を区別できません。経路とIPを信頼できるネットワークだけで使ってください。配布サーバーの前にプロキシを置くと、接続元がすべてプロキシのIPになります。

## クライアント項目

`distribution.clients` の各要素に指定できる項目です。

| 項目 | 必須・条件 | 内容 |
| --- | --- | --- |
| `id` | 必須 | クライアントID。空や重複は不可。クライアントは `client.id` に同じ値を設定する |
| `enabled` | 必須（省略時はfalse） | falseのクライアントの要求は拒否する。登録したまま止めたいときに使う |
| `auth` | 任意 | `token`・`mtls`・`ip`。省略すると `distribution.auth` を使う |
| `token_hash_file` | `token` で必須。ほかの方式では指定不可 | トークンのSHA-256（小文字16進64桁）を1行で書いたファイルの絶対パス。[トークンの作成](manual-distribution.md#3-手動でクライアント資格情報を作る) |
| `client_certificate_sha256` | `tls.client_ca` を設定した場合に必須 | クライアント証明書のSHA-256の配列。ローテーション中は新旧を並べる |
| `inventory_access` | 台帳モードだけ | 取得できる範囲（下表）。省略時は `ip` |
| `allowed_certificates` | 台帳モードでは `names`・`ip_and_names` だけで指定。台帳を使わない構成では取得範囲を決める（`ip` 方式では必須） | 取得を許す証明書名の配列。ワイルドカードは使えない |
| `certificate_access_overrides` | 任意。台帳モードの `ip`・`ip_and_names` だけ | 証明書名ごとに、取得を追加で許す台帳ホスト名。[例外設定](dns-inventory.md#サーバーの設定) |
| `allowed_ips` | 台帳を使わない構成で指定（台帳モードでは指定不可） | 許可する接続元のIPまたはCIDR。一致しない接続元は拒否する。`ip` 方式では単一IPだけ |

台帳モードの `inventory_access`:

| 値 | 取得できる証明書 | `auth: ip` で使えるか |
| --- | --- | --- |
| `ip` | 台帳上のIPが接続元IPと一致する名前 | 使える |
| `ip_and_names` | `ip` の条件を満たし、`allowed_certificates` にも列挙した名前 | 使える |
| `names` | `allowed_certificates` に列挙した名前（接続元は問わない） | 使えない |
| `all` | 台帳上のすべての名前 | 使えない |

## 設定例

### トークン（既定）

```yaml
distribution:
  clients:
    - id: "web-server-01"
      enabled: true
      token_hash_file: "/etc/certpouch/tokens/web-server-01.sha256"
      inventory_access: "ip_and_names"
      allowed_certificates: ["web01.pvt.example.internal"]
```

クライアント側:

```yaml
client:
  id: "web-server-01"
  token_file: "/etc/certpouch/client.token"
```

### IPだけ（台帳モード）

サーバー全体をIP方式にする例です。台帳上のIPと接続元IPが一致する名前だけを取得できます。

```yaml
distribution:
  auth: ip
  clients:
    - id: "web-server-01"
      enabled: true
      inventory_access: "ip_and_names"
      allowed_certificates: ["web01.pvt.example.internal"]
```

クライアント側では `auth: ip` を指定し、`token_file` は書きません。

```yaml
client:
  id: "web-server-01"
  auth: ip
```

### IPだけ（台帳を使わない構成）

台帳がない場合は、単一のIP（`/32`・`/128` を含む）と、取得を許す証明書名の両方を指定します。CIDRで範囲を許可する設定は拒否されます。

```yaml
distribution:
  clients:
    - id: "web-server-01"
      enabled: true
      auth: ip
      allowed_ips: ["192.0.2.10"]
      allowed_certificates: ["web.example.internal"]
```

### 方式を混在させる

既定はトークンのまま、特定のクライアントだけIP方式にできます。

```yaml
distribution:
  auth: token
  clients:
    - id: "web-server-01"
      enabled: true
      token_hash_file: "/etc/certpouch/tokens/web-server-01.sha256"
    - id: "lab-printer"
      enabled: true
      auth: ip
      inventory_access: "ip_and_names"
      allowed_certificates: ["printer.pvt.example.internal"]
```

## 登録と変更の反映

- クライアントの追加・変更・削除、`auth` や `token_hash_file` の変更は、配布サーバーの再起動で反映します。台帳のIPの変更は、再起動しなくても `distribution.refresh_interval` ごとに反映されます。
- 変更前に、新しい設定で `certpouch server --check-config --config <新しいserver.yaml>` を実行します。DNSと発行も同じ `server.yaml` で動かしている場合は、`certpouch pipeline --check` も通してください。
- 再起動の直後は、HTTPSが応答するまで少し時間がかかります。ヘルスチェックは一度で失敗と判断せず、数十秒は再試行します。
- クライアントが1つもない状態（`clients: []`）でも起動できます。待ち受けをループバックにして先に起動しておき、クライアントの登録時に、利用側から届くアドレスへ変更できます。

## 認証方式を切り替える

トークン方式で稼働中のクライアントを、IP方式へ切り替える例です。mTLS方式への切り替えも同じ順で進めます。

1. サーバーとクライアントのバイナリを、認証方式を選べる版（v2.1.0以降）に揃えます。`auth` を省略した設定はトークン方式のまま動くので、先に更新しても取得は止まりません。
2. サーバー側で `auth: ip` を設定し、対象クライアントの `token_hash_file` を外します。`inventory_access` が `ip` か `ip_and_names` であることを確認します。
3. 新しい設定を検証してから置き換え、配布サーバーを再起動します。
4. クライアント側で `client.auth: ip` を設定して `client.token_file` を外し、`certpouch pull --dry-run` で確認してから置き換えます。

> [!CAUTION]
> 手順3から4までの間、クライアントはトークンを送り続けるため、取得に失敗します。定期実行の時刻を避けて作業し、手順4まで続けて行ってください。

切り替えた後に確認すること:

| 要求 | 期待する応答 |
| --- | --- |
| IDを付けない | 401 |
| 登録していないIDを付ける | 401 |
| 登録済みのIDを、台帳と異なる接続元IPから付ける | 401 |
| 許可していない証明書名を取得する | 404 |
| 対象クライアントからの `pull --dry-run` | 許可した証明書だけが表示される |

不要になったトークンファイル（クライアント側）とハッシュファイル（サーバー側）は削除します。

## 関連

- [DNS台帳による接続元制御](dns-inventory.md)
- [配布APIの相互TLS](mtls.md)
- [クライアントの失効](client-revocation.md)
- [設定仕様の配布認証](SPEC/02-configuration.md#配布認証の設定)
