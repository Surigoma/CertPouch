# CLIで初期設定を作る

`certpouch configure` は設定を新規ディレクトリへ生成する。既存ディレクトリやファイルは上書きしない。生成したYAMLは厳密デコーダーと各機能の検証処理を通し、失敗時は作成途中のディレクトリを削除する。

## DNSとSheetsの設定

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

生成物は次のとおり。

| ファイル | 条件 | 用途 |
| --- | --- | --- |
| `dns.yaml` | 常に生成 | `dns sync` の設定 |
| `fetch.yaml` | Sheetsの3項目を指定 | `dns fetch` の設定 |

キャッシュ内容、固定レコード、既存SOA serialとの比較は生成後に別途確認する。初回適用後は[DNSゾーン生成と適用](dns-sync.md)に従い、管理中のserialを引き継ぐ。

## 配布サーバーとクライアント

既存の初期化機能も同じ入口から利用できる。

```sh
certpouch configure server --help
certpouch configure client --help
```

これらは `init server`、`init client` と同じ検証・生成処理を使う。トークンを画面へ表示せず、新規ディレクトリだけに設定と資格情報を生成する。
