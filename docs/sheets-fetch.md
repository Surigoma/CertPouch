# Google SheetsからのDNS台帳取得

`certpouch dns fetch` は、Google Sheets APIから既存形式のDNS台帳を読み取り、JSONのスナップショットを更新します。

- 認証は `golang.org/x/oauth2/google`、範囲の取得は `google.golang.org/api/sheets/v4` で行います。[公式Sheetsクライアント](https://pkg.go.dev/google.golang.org/api/sheets/v4)
- Sheetsへの書き込み、NSDへの適用、証明書の配布設定の変更は行いません。

## 設定

[サーバー設定例](../configs/server.example.yaml)で、次を設定します。

| 項目 | 内容 |
| --- | --- |
| `inventory.cache_file` | キャッシュの出力先 |
| `inventory.sheets.spreadsheet_id` | スプレッドシートID |
| `inventory.sheets.ranges` | シートごとの範囲。`'Example inventory'!A2:G256` のように、シート名と、見出し行を除いたセル範囲を指定します。[DNS台帳の記入例](dns-inventory.md#台帳の記入例) |
| `inventory.sheets.credentials_file` | サービスアカウントのJSONファイル |

**資格情報の保護**

- 対象のスプレッドシートには、サービスアカウントに閲覧権限だけを付けます。[サービスアカウントの認証方式](https://developers.google.com/identity/protocols/oauth2/service-account)
- JSONファイルは0600にし、取得用ユーザーだけが読めるようにします。
- 取得コマンドは読み取り専用のOAuthスコープを使い、サービスアカウントのJSONや応答本文をエラーに表示しません。

## 取得する

```sh
certpouch dns fetch --config /etc/certpouch/server.yaml
```

1. 対象のシートID、指定したすべての範囲とその順番、列数、台帳の内容を確認します。
2. 確認に通った場合だけ、Linux上でキャッシュを置き換えます。キャッシュは、検証したAPI応答をJSONに整形して保存します。
3. 最終成功時刻とキャッシュのSHA-256を `cache.json.freshness.json` に記録します。内容が同じ場合は、台帳本体は変えずに記録だけを更新します。
4. 出力には、更新の有無と名前の件数だけを表示します。

次の場合は、以前のキャッシュと最終成功時刻を維持します。

- 取得障害
- 範囲の欠落
- 不正な台帳
- すべての名前が空のスナップショット

## 状態を確認する

```sh
certpouch dns fetch --status --config /etc/certpouch/server.yaml
```

`--status` はGoogleへ接続せず、サービスアカウントも読まずに、最終成功時刻とキャッシュの状態をJSONで返します。`inventory.max_age: "45m"` のように設定すると、期限も確認できます。

| 状態 | 終了コード |
| --- | --- |
| `verified`（検証済み） | 0 |
| `stale`、`untracked`、`missing`、`invalid` | 1 |
| 設定エラー | 2 |

## キャッシュの権限

- キャッシュの親ディレクトリは事前に作成し、取得ユーザーに通過・書き込み権限を与えます。
- 既存のキャッシュを使う場合は、取得ユーザーの所有にします。
- 配布サーバーが別ユーザーなら、取得用と配布用に共有グループを割り当て、親ディレクトリの通過権限とファイルの0640を設定します。
- 新規作成時の既定は0600です。既存ファイルのモードとグループは、更新時に引き継ぎます。
- キャッシュの書き手は1つにしてください。

## ロックファイル

| ファイル | 役割 | 注意 |
| --- | --- | --- |
| `cache.json.fetch-guard` | 取得中の排他。プロセスが終了すると解放されます | 稼働中は取り除かないでください |
| `cache.json.commit-lock` | 鮮度を検証する読み込みと、2ファイルの置き換えが重ならないようにします | 稼働中は保持してください |
| `cache.json.fetch-lock`（旧版） | 旧版の排他ディレクトリ | 新しいバイナリの取得は妨げません |

- 新しいバイナリに切り替える前に、旧版の同期・取得処理を停止してください。
- 既存のキャッシュと取得記録は、そのまま読み取れます。読み込みと書き込みの両方を `commit-lock` に対応した版へ更新すると、同時実行時にも検証できます。
- 保存中の異常終了などで2つのファイルが食い違った場合は、次の正常な取得で解消します。

## 定期取得

[取得用service](../configs/systemd/certpouch-sheets-fetch.service)と[timer](../configs/systemd/certpouch-sheets-fetch.timer)は、15分ごとに取得する例です。

1. 専用の `certpouch-sheets` ユーザーを用意し、キャッシュの出力先と権限を合わせます。
2. 手動で取得が成功することを確認します。
3. unitを配置して有効にします。

   ```sh
   sudo systemctl daemon-reload
   sudo systemctl enable --now certpouch-sheets-fetch.timer
   ```

台帳上のすべての名前を発行する構成（`certificates.targets` を省略）では、このタイマーは使わず `pipeline --watch` を使います。[証明書更新](certificate-renewal.md)

## 取得後の流れ

- 新しい台帳からゾーンを作るには、[DNSゾーン生成と適用](dns-sync.md)を参照してください。取得したキャッシュから候補を確認してから、DNSの変更を適用します。
- 配布サーバーは `distribution.refresh_interval` ごとに台帳を読み直し、認可に反映します。[配布サーバーの台帳設定](dns-inventory.md)
