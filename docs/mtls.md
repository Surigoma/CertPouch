# 配布APIの相互TLS

相互TLS（mTLS）を有効にすると、配布サーバーはHTTPS接続の確立時にクライアント証明書を検証します。

- APIは、検証済みのリーフ証明書のSHA-256フィンガープリントを、クライアントIDの設定と照合します。
- 従来のトークン認証と、証明書ごとの接続元IP認可も引き続き行います。

## サーバーの設定

1. `distribution.tls.client_ca` に、クライアント証明書を検証するCAバンドルの絶対パスを指定します。可能であれば、配布するサーバー証明書とは別のクライアント用CAを使ってください。
2. 有効な各クライアントに `client_certificate_sha256` を追加します。

```yaml
distribution:
  tls:
    certificate_file: "/etc/certpouch/tls/server.crt"
    private_key_file: "/etc/certpouch/tls/server.key"
    client_ca: "/etc/certpouch/tls/client-ca.pem"
  clients:
    - id: "web-server-01"
      enabled: true
      token_hash_file: "/etc/certpouch/credentials/web-server-01.sha256"
      client_certificate_sha256:
        - "<リーフ証明書のSHA-256を小文字の16進64桁で指定>"
```

この抜粋を既存の `server.yaml` に追加します。HTTPSの待ち受け、公開ストア、証明書の検証条件、接続元のルールも設定してください。[サーバー設定仕様](SPEC/02-configuration.md)

フィンガープリントは、リーフ証明書をDERに変換して計算します。

```sh
openssl x509 -in client.crt -outform DER | openssl dgst -sha256
```

### 証明書のローテーション

有効なクライアントの間で、同じフィンガープリントは共有できません。

1. 同じクライアントIDに、旧・新両方のフィンガープリントを登録して再起動します。
2. クライアントに新しい証明書を配布し、動作を確認します。
3. 旧フィンガープリントを削除して再起動します。

漏えいした証明書を再起動なしで拒否するには、[クライアントの失効](client-revocation.md)を設定します。

## クライアントの設定

```yaml
client:
  tls_certificate: "/etc/certpouch/client.crt"
  tls_private_key: "/etc/certpouch/client.key"
```

- `client.tls_certificate` と `client.tls_private_key` に、クライアント用のPEM証明書と秘密鍵の絶対パスを両方設定します。
- クライアント証明書は、Client Authentication用途で発行します。
- 中間CAがある場合は、リーフの後ろに中間CAを添付します。
- 秘密鍵は、既定の保護に従って0600で保管します。
- 配布サーバーの証明書を検証する `server.ca_file` は、引き続き必要です。

## 有効にする前の確認

- 相互TLSが有効な間は、healthを含むすべてのHTTPS接続に、信頼されたクライアント証明書が必要です。
- APIのIDに紐付けていない証明書は、配布操作で401になります。
- クライアント証明書を持たない既存の監視から切り替える場合は、監視にもクライアント証明書を用意してください。
- 配布先サービスのTLS確認（`verify_tls`）と、配布APIへの相互TLSは別の接続です。
