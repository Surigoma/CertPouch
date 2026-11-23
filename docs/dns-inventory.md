# DNS台帳による接続元制御

既存のゾーン生成処理がGoogle Sheetsの取得結果として保存する `cache.json` を参照できる。サーバーは起動時にスナップショットを読み込む。認証済みのクライアントには、接続元IPが証明書の論理名に対応するDNS台帳のIPと一致する場合に、その証明書の一覧表示・詳細取得・ダウンロードを許可する。

サーバー設定に次の項目を加える。クライアントごとの `allowed_ips`、`allowed_certificates`、旧案の `allowed_hosts` はDNS台帳モードでは削除する。サーバーの `certificates` は引き続き公開対象とSAN・発行者の検証に使う。例のパス、ゾーン、初期IPv4プレフィックスは環境に合わせて設定する。

```yaml
dns_inventory:
  cache_file: "/var/lib/dns-inventory/cache.json"
  fallback_prefix: "192.0.2"
  zone: "example.internal"
clients:
  - id: "web-server-01"
    enabled: true
    token_hash_file: "/etc/certpouch/credentials/web-server-01.sha256"
    # 任意。通常の接続元に加えて、指定した台帳ホストに取得を許可する。
    certificate_access_overrides:
      web.example.internal: ["backup.pvt.example.internal"]
```

対象範囲ごとにB列のIPv4プレフィックスを保持し、C列の末尾オクテットと連結する。A列が空でC列に値がある行を対象とし、D列の名前には `.pvt` とゾーン名を補う。G列のカンマ区切りの別名にもゾーン名を補う。現在のアダプターはASCIIのホスト名を扱う。国際化ドメイン名は未対応。

既定では、台帳に同名のレコードがない証明書へのアクセスは拒否する。例外設定のホストが台帳にない場合やファイルが不正な場合は起動に失敗する。スナップショットを変更したらサーバーを再起動する。既存処理はシート内容が変わったときにだけキャッシュを書き換えるため、ファイル時刻だけでは台帳への最終アクセス成功時刻を判定できない。Google Sheets APIへの直接接続と稼働中の更新反映は今後の作業とする。
