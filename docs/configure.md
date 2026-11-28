# CLIで初期設定を作る

`certpouch configure` で、検証済みの初期設定を作成する方法を説明します。初回接続から日常のDNS更新、証明書の発行・配置までの流れは[利用シーン別の操作例](user-flows.md)を参照してください。

## 生成される設定

| 側 | ファイル | 形式 |
| --- | --- | --- |
| サーバー | `server.yaml` 1つ | [サーバー設定例](../configs/server.example.yaml)と同じv2形式。`configure dns` と `configure server` もこの形式で出力します |
| クライアント | `client.yaml` | [client.yaml の例](../configs/client.example.yaml) |

`server.yaml` の4つの節:

| 節 | 内容 |
| --- | --- |
| `inventory` | 台帳の入力元 |
| `dns` | 管理ゾーンとNSD |
| `certificates` | 既存CAと、発行・配布する名前 |
| `distribution` | HTTPS配布と取得制限 |

項目の一覧は[設定仕様](SPEC/02-configuration.md)を参照してください。

`configure` の共通の動作:

- 設定は新規ディレクトリへ生成します。既存のディレクトリやファイルは上書きしません。
- 生成したYAMLは、厳密なデコーダーと各機能の検証処理を通します。失敗した場合は、作成途中のディレクトリを削除します。
- トークンを画面に表示しません。

CertPouch v1の `server`、`storage`、`dns_inventory`、`google_sheets`、`zones`、`renewal`、`pipeline`、`clients` を使った設定は読み込めません。起動時のエラーに表示される移行先のキーへ書き換えてください。

## DNSとSheetsの設定

最初は `server.yaml` の `inventory` と `dns` だけで候補を確認できます。[全体の設定例](../configs/server.example.yaml)から、使う段階に合わせて節を追加してください。

| 段階 | 編集する節 | 入力する内容 |
| --- | --- | --- |
| Sheets取得 | `inventory` と `dns.zone` | キャッシュの絶対パス、正引きゾーン名、IPv4の既定プレフィックス、シートID・範囲、閲覧専用サービスアカウントJSONの絶対パス |
| ゾーン候補の確認 | `dns.zones` | 現行NSDのSOA・NS・TTL。serialは書かない |
| NSD反映 | `dns.nsd` | 候補と管理世代の保存先、`nsd.conf`、専用include、SOA確認用のループバックアドレス |
| 証明書の発行・配布 | `certificates` と `distribution` | 既存CAへのパス、共通の有効期間と更新時期、保存先、HTTPSとクライアント認可 |

### 対話入力で作る

```sh
certpouch configure dns --interactive
```

1. 出力先、キャッシュ、ゾーン、IPv4の既定プレフィックス、SOA primaryを順に入力します。
2. SOA mailboxとnameserverは、表示される既定値をEnterで採用できます。
3. Google Sheets取得を使う場合は、続けてspreadsheet ID、サービスアカウントJSONの絶対パス、rangeを入力します。

秘密鍵やサービスアカウントJSONの内容は入力しません。

### フラグで作る

自動化では、すべてをフラグで指定できます。

```sh
certpouch configure dns \
  --out /etc/certpouch-dns \
  --cache /var/lib/dns-inventory/cache.json \
  --zone example.internal \
  --fallback-prefix 192.0.2 \
  --primary ns1.example.internal \
  --spreadsheet-id REPLACE_WITH_SPREADSHEET_ID \
  --service-account /etc/certpouch-sheets/service-account.json \
  --range "'Example inventory'!A2:G256"
```

| フラグ | 省略時・補足 |
| --- | --- |
| `--nameserver` | 複数回指定できます。省略するとSOA primaryを使います |
| `--range` | 複数回指定できます |
| `--mailbox` | 省略すると `hostmaster.<zone>` を使います |
| `--ttl` | 既定は300です |

### 生成結果を確認する

生成物はサーバー側の `server.yaml` だけです。Sheetsの3項目を指定した場合は、同じファイルに `inventory.sheets` も入ります。取得元、キャッシュ、ゾーン生成条件を1つのファイルで管理するため、二重に更新する必要はありません。

キャッシュの親ディレクトリを作成し、サービスアカウントJSONを取得ユーザーだけが読めるようにしてから、同じ `server.yaml` を両方のコマンドに渡します。

```sh
certpouch dns fetch --config /etc/certpouch/server.yaml
certpouch dns sync --config /etc/certpouch/server.yaml --dry-run
```

- `--dry-run` は正引き・逆引きの候補を表示します。
- キャッシュの内容、固定レコード、既存のSOA serialとの比較は、生成後に別途確認してください。
- NSDの初期登録とその後の反映は、[ゾーン生成・適用手順](dns-sync.md)に沿って行います。

### serialの扱い

- SOA serialは設定に書きません。
- serialを指定しないプレビューはserial 1で表示し、その旨を標準エラーに出します。
- 初回の候補は `dns sync --serial N` で作ります。
- 管理世代を持った後は、`pipeline` が配信中の世代からserialを引き継ぎます。

## 配布サーバーとクライアント

配布サーバーと取得ホスト1台分の初期設定も、同じ入口から作成できます。`configure server` は、`certificates.targets` に1件の名前を持つ `server.yaml` を出力します。手順は[手動配布の手順2](manual-distribution.md#2-configure-で初期設定を作る)を参照してください。

```sh
certpouch configure server --help
certpouch configure client --help
```

名前と発行者の設定:

- 名前を固定して配布する場合は、サーバーの `certificates.targets[].name` とクライアントの `certificates[].name` にDNS名を書くだけで、その名前が期待DNS SANになります。
- 共通の直接発行者は、サーバーの `certificates.issuer.certificate_file` と、クライアントの `validation.issuer_certificate` に設定します。
- 別名・IP SAN・別の発行者が必要な証明書だけ、個別に設定してください。
- `certificates.targets` を省略すると、台帳上のすべての名前が対象になります。[設定例](../configs/server.example.yaml)
