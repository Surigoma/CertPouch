# DNS台帳による接続元制御

既存のゾーン生成処理がGoogle Sheetsの取得結果として保存する `cache.json` を参照できる。サーバーは起動時にスナップショットを読み込む。認証済みのクライアントには、接続元IPが証明書の論理名に対応するDNS台帳のIPと一致する場合に、その証明書の一覧表示・詳細取得・ダウンロードを許可する。

台帳をCSVで出力している場合は、`cache_file` の代わりに `csv_files: ["/var/lib/dns-inventory/export.csv"]` を指定できる。複数のCSVを指定すると、シートごとにIPv4プレフィックスをリセットして同じ列規則で読む。

`certpouch dns fetch` を使い、Google Sheetsからキャッシュを直接更新することもできる。[取得設定と手順](sheets-fetch.md)

サーバー設定に次の項目を加える。台帳モードではクライアントごとの `allowed_ips` と `allowed_certificates` を省く。サーバーの `certificates` には公開対象とSAN・発行者の検証条件を設定する。`init server` でも台帳モードの設定を生成できる。[初回導入](quickstart.md)

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

## 台帳の記入例

次の例では `zone: example.internal` を使います。1行目は見出しで、Google Sheetsから取得する範囲は `A2:G4` です。表の空欄はシートでも空のセルにします。

| A 除外マーク | B IPv4プレフィックス | C IPv4末尾 | D 機器名 | E 自由欄 | F 自由欄 | G 別名 |
| --- | --- | --- | --- | --- | --- | --- |
|  | 192.0.2 | 10 | web01 |  |  | web, portal |
|  |  | 11 | backup |  |  |  |
| x |  | 12 | retired |  |  | old |

- `web01` の行は `web01.pvt.example.internal`、`web.example.internal`、`portal.example.internal` を `192.0.2.10` に対応付けます。
- `backup` の行は直前のIPv4プレフィックスを使い、`backup.pvt.example.internal` を `192.0.2.11` に対応付けます。
- `retired` は除外マークがあるため台帳に入りません。証明書名が `web.example.internal` の場合、通常は `192.0.2.10` から取得できます。設定例の例外を追加すると、`backup` のIPからも取得できます。

プレフィックスをまだ指定していない行には、設定の `fallback_prefix` を使います。プレフィックスの引き継ぎは取得範囲またはCSVごとにリセットします。末尾オクテットや機器名が空の行はアドレスを生成しません。E・Fの内容は読み取りに使いません。ホスト名はASCIIで入力してください。

CSVで同じ表を使う場合は、別名のカンマを含むセルを二重引用符で囲みます。

```csv
除外マーク,IPv4プレフィックス,IPv4末尾,機器名,自由欄,自由欄,別名
,192.0.2,10,web01,,,"web, portal"
,,11,backup,,,
x,,12,retired,,,old
```

既定では、台帳に同名のレコードがない証明書へのアクセスは拒否する。例外設定のホストが台帳にない場合やファイルが不正な場合は起動に失敗する。

`server.inventory_refresh: "1m"` のように設定すると、起動後も台帳を定期的に読み直してIPによる取得許可を入れ替える。読み込みや例外設定の再解決に失敗した場合は、台帳モードの認証を一時停止し、正常な台帳を読めた時点で再開する。公開ストアの更新とは別に処理するため、新しい証明書が不正でも古いIPへの許可は取り消せる。トークン、適用する設定ファイル、例外ホスト名を変更した場合はサーバーを再起動する。

`inventory_refresh` を省略すると、台帳変更の反映にはサーバー再起動が必要。取得が途絶えたキャッシュを拒否したい場合は、`dns fetch` でキャッシュを管理し、配布サーバーの `dns_inventory.max_age` を設定する。たとえば15分間隔で取得するなら `max_age: "45m"` にすると、取得の遅延に余裕を持たせられる。

この設定では、キャッシュと最終成功記録のSHA-256が一致し、記録の時刻が現在から `max_age` 未満の場合だけ認可に使う。期限に達した時点で、次の台帳読み込みを待たずに新規リクエストを拒否する。正常な取得後、次の定期読み込みで認可を再開する。CSVと旧Pythonキャッシュでは、`max_age` を省いて既存の方式を使える。
