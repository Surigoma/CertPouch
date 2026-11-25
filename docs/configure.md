# CLIで初期設定を作る

`certpouch configure` は設定を新規ディレクトリへ生成する。既存ディレクトリやファイルは上書きしない。生成したYAMLは厳密デコーダーと各機能の検証処理を通し、失敗時は作成途中のディレクトリを削除する。

## DNSとSheetsの設定

最初は `server.yaml` の `pipeline.dns` だけで候補を確認できます。[全体の設定例](../configs/server.example.yaml)から、使う段階に合わせて節を追加してください。

| 段階 | 編集する節 | 入力する内容 |
| --- | --- | --- |
| Sheets取得 | `pipeline.dns.inventory` と `pipeline.dns.sheets` | キャッシュの絶対パス、正引きゾーン名、IPv4の既定プレフィックス、シートID・範囲、閲覧専用サービスアカウントJSONの絶対パス |
| ゾーン候補の確認 | `pipeline.dns.zones` | 現行NSDのSOA・NS・TTL。初回の候補確認では `serial` も指定 |
| NSD反映 | `pipeline.dns.deployment` | 候補の保存先、管理世代の保存先、`nsd.conf`、SOA確認用のループバックアドレス |
| 証明書の発行・配布 | `pipeline.certificates.renewal`、`server`、`storage`、`certificates`、`clients` | 既存CAへのパス、発行プロファイル、HTTPS・保存先・証明書ポリシー・接続元認可 |

仮設定では、キャッシュの親ディレクトリを作ってから同じ `server.yaml` を両方に渡します。サービスアカウントJSONは取得ユーザーだけが読めるようにします。

```sh
certpouch dns fetch --config /etc/certpouch/server.yaml
certpouch dns sync --config /etc/certpouch/server.yaml --dry-run
```

`--dry-run` は正引き・逆引き候補を表示します。NSDの初期登録とその後の反映は[ゾーン生成・適用手順](dns-sync.md)に沿って行います。管理開始後は固定の `serial` を外し、管理中の世代から引き継ぎます。証明書の `renewal.profiles[].name` はトップレベルの `certificates[].name` と揃えます。

CUIでDNS設定を作成する。

```sh
certpouch configure dns --interactive
```

出力先、キャッシュ、ゾーン、IPv4の既定プレフィックス、SOA primaryを順に入力する。SOA mailboxとnameserverは表示される既定値をEnterで採用できる。Google Sheets取得を使う場合は、続けてspreadsheet ID、サービスアカウントJSONの絶対パス、rangeを入力する。秘密鍵やサービスアカウントJSONの内容は入力しない。

自動化ではすべてインラインで指定できる。

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

`--nameserver` と `--range` は複数回指定できる。`--mailbox` を省略すると `hostmaster.<zone>`、`--nameserver` を省略するとSOA primaryを使う。TTLは300、初回serialは1を既定とし、`--ttl` と `--serial` で変更できる。

生成物はサーバー側の共通設定 `server.yaml` だけである。Sheetsの3項目を指定した場合は同じファイルに
`pipeline.dns.sheets` も入り、`dns fetch` と `dns sync` の両方へ同じパスを渡す。
取得元、キャッシュ、ゾーン生成条件が一つの設定で管理されるため、重複更新は不要になる。


キャッシュ内容、固定レコード、既存SOA serialとの比較は生成後に別途確認する。初回適用後は[DNSゾーン生成と適用](dns-sync.md)に従い、管理中のserialを引き継ぐ。

## 配布サーバーとクライアント

通常の証明書では `certificates[].name` にDNS名を書くだけで、その名前が期待DNS SANになります。共通の直接発行者はサーバーの `storage.issuer_certificate`、クライアントの `validation.issuer_certificate` に設定します。別名・IP SAN・別の発行者が必要な証明書だけ、個別に設定してください。[設定例](../configs/server.example.yaml)

既存の初期化機能も同じ入口から利用できる。

```sh
certpouch configure server --help
certpouch configure client --help
```

これらは `init server`、`init client` と同じ検証・生成処理を使う。トークンを画面へ表示せず、新規ディレクトリだけに設定と資格情報を生成する。
