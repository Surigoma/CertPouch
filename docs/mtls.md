# 配布APIの相互TLS

相互TLSを有効にすると、配布サーバーはHTTPS接続の確立時にクライアント証明書を検証する。APIは検証済みリーフ証明書のSHA-256フィンガープリントをクライアントIDの設定と照合し、従来のトークン認証と証明書ごとの接続元IP認可も行う。

## サーバーの設定

サーバー設定の `server.tls.client_ca` に、クライアント証明書を検証するCAバンドルの絶対パスを指定する。可能であれば、配布対象のサーバー証明書とは別のクライアント用CAを使用する。各有効クライアントに `client_certificate_sha256` を追加する。

```yaml
server:
  tls:
    certificate: "/etc/certpouch/server.crt"
    private_key: "/etc/certpouch/server.key"
    client_ca: "/etc/certpouch/client-ca.pem"
clients:
  - id: "web-server-01"
    enabled: true
    token_hash_file: "/etc/certpouch/credentials/web-server-01.sha256"
    client_certificate_sha256:
      - "<リーフ証明書のSHA-256を小文字の16進64桁で指定>"
```

例では既存の証明書配布設定と接続元ルールを省略している。フィンガープリントはリーフ証明書をDERに変換して計算する。

```sh
openssl x509 -in client.crt -outform DER | openssl dgst -sha256
```

有効なクライアント間で同一フィンガープリントを共有できない。ローテーションでは同じクライアントIDに旧・新フィンガープリントを登録し、再起動する。クライアントに新証明書を配布して動作を確認したあと、旧フィンガープリントを削除して再起動する。

## クライアントの設定

`client.tls_certificate` と `client.tls_private_key` にクライアント用PEM証明書と秘密鍵の絶対パスを設定する。クライアント証明書はClient Authentication用途で発行する。中間CAがある場合はリーフの後ろに中間CAを添付する。秘密鍵は既定の保護に従い0600で保管する。配布サーバーの証明書を検証する `server.ca_file` は引き続き必要。

相互TLSが有効な間は、healthを含むすべてのHTTPS接続に信頼されたクライアント証明書が必要。APIのIDに紐付けていない証明書は配布操作で401になる。クライアント証明書のない既存の監視から切り替える場合は、監視にもクライアント証明書を用意する。配布先サービスのTLS確認と、配布APIへの相互TLSは別の接続である。
