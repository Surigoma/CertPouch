# DNS台帳による接続元制御

DNS台帳を使って、証明書を取得できる接続元を制御する方法です。

認証済みのクライアントは、接続元IPが証明書の論理名に対応する台帳のIPと一致する場合に、その証明書の一覧表示・詳細取得・ダウンロードができます。

## 台帳の入力元

| 入力元 | 設定 |
| --- | --- |
| Google Sheetsの取得結果のJSONキャッシュ（既存のゾーン生成処理が保存する `cache.json` も可） | `inventory.cache_file` |
| `certpouch dns fetch` でGoogle Sheetsから直接更新するキャッシュ | `inventory.cache_file`。[取得設定と手順](sheets-fetch.md) |
| CSVエクスポート | `inventory.csv_files: ["/var/lib/certpouch/export.csv"]`。複数指定すると、シートごとにIPv4プレフィックスをリセットして同じ列規則で読みます |

サーバーは起動時に台帳のスナップショットを読み込みます。

## サーバーの設定

`server.yaml` に次の項目を加えます。

- 台帳モードでは、クライアントごとの `allowed_ips` を書きません。
- 例はトークン方式です。mTLSやIPだけの方式、`inventory_access` の選び方は[配布クライアントと認証方式](distribution-clients.md)を参照してください。
- 配布する名前は、`certificates.targets` で固定するか、省略して台帳上のすべての名前を対象にします。
- `configure server` でも台帳モードの設定を生成できます。[手動配布の手順](manual-distribution.md)

```yaml
inventory:
  cache_file: "/var/lib/certpouch/inventory.json"
  fallback_prefix: "192.0.2"
dns:
  zone: "example.internal"
distribution:
  clients:
    - id: "web-server-01"
      enabled: true
      token_hash_file: "/etc/certpouch/tokens/web-server-01.sha256"
      # 任意。通常の接続元に加えて、指定した台帳ホストに取得を許可する。
      certificate_access_overrides:
        web.example.internal: ["backup.pvt.example.internal"]
```

- 台帳に同名のレコードがない証明書へのアクセスは、既定で拒否します。
- 台帳ファイルが不正な場合は、起動に失敗します。
- `certificates.targets` で配布名を固定する場合は、例外設定の証明書名を `targets` に、例外ホストを台帳に登録してください。起動時にどちらかの登録が欠けていると、設定エラーになります。
- 台帳から発行対象を決める場合は、台帳に存在する証明書名と例外ホストの組み合わせだけが有効です。台帳から消えた名前に対応する例外許可は次の読み直しで外れ、ほかの有効な許可は維持されます。

## 台帳の記入例

`zone: example.internal` の例です。1行目は見出しで、Google Sheetsから取得する範囲は `A2:G4` です。表の空欄は、シートでも空のセルにします。

| A 除外マーク | B IPv4プレフィックス | C IPv4末尾 | D 機器名 | E 自由欄 | F 自由欄 | G 別名 |
| --- | --- | --- | --- | --- | --- | --- |
|  | 192.0.2 | 10 | web01 |  |  | web, portal |
|  |  | 11 | backup |  |  |  |
| x |  | 12 | retired |  |  | old |

この台帳から次の対応ができます。

| 行 | 生成される名前 | IP |
| --- | --- | --- |
| `web01` | `web01.pvt.example.internal`、`web.example.internal`、`portal.example.internal` | `192.0.2.10` |
| `backup` | `backup.pvt.example.internal`（直前の行のプレフィックスを引き継ぐ） | `192.0.2.11` |
| `retired` | なし（除外マークがあるため台帳に入らない） | — |

証明書名が `web.example.internal` の場合、通常は `192.0.2.10` から取得できます。上の設定例の例外を加えると、`backup` のIPからも取得できます。

### 列の規則

- 機器名（D列）は `<機器名>.pvt.<zone>`、別名（G列）は `<別名>.<zone>` になります。複数の別名はカンマで区切ります。
- プレフィックスをまだ指定していない行には、設定の `fallback_prefix` を使います。
- プレフィックスの引き継ぎは、取得範囲またはCSVごとにリセットします。
- 末尾オクテットや機器名が空の行は、アドレスを生成しません。
- E・F列の内容は読み取りに使いません。
- ホスト名はASCIIで入力してください。

### CSVの場合

別名のカンマを含むセルは、二重引用符で囲みます。

```csv
除外マーク,IPv4プレフィックス,IPv4末尾,機器名,自由欄,自由欄,別名
,192.0.2,10,web01,,,"web, portal"
,,11,backup,,,
x,,12,retired,,,old
```

## 台帳の読み直し

配布サーバーは `distribution.refresh_interval`（既定5秒）ごとに台帳を読み直し、IPによる取得許可を入れ替えます。

- 読み込みや例外設定の再解決に失敗した場合は、台帳モードの認証を一時停止し、正常な台帳を読めた時点で再開します。
- 公開ストアの更新とは別に処理するため、新しい証明書が不正でも、古いIPへの許可は取り消せます。
- トークン、設定ファイル、例外のホスト名を変更した場合は、サーバーを再起動します。

## 古い台帳を拒否する

取得が途絶えたキャッシュを認可に使わないようにするには、`dns fetch` または `pipeline` でキャッシュを管理し、`inventory.max_age` を設定します。たとえば15分間隔で取得するなら、`max_age: "45m"` にすると取得の遅延に余裕を持たせられます。

- キャッシュと最終成功記録のSHA-256が一致し、記録の時刻が現在から `max_age` 未満の場合だけ、認可に使います。
- 期限に達した時点で、次の台帳読み込みを待たずに新規リクエストを拒否します。
- 正常に取得できた後、次の定期読み込みで認可を再開します。
- CSVと旧Pythonのキャッシュでは、`max_age` を省いて従来の方式で使えます。
