# Google SheetsからのDNS台帳取得

`certpouch dns fetch` はGoogle Sheets APIから既存形式のDNS台帳を読み取り、JSONスナップショットを更新する。認証は `golang.org/x/oauth2/google`、範囲取得は `google.golang.org/api/sheets/v4` に任せる。Sheetsへの書き込み、NSDへの適用、証明書の配布設定の変更は行わない。[公式Sheetsクライアント](https://pkg.go.dev/google.golang.org/api/sheets/v4)

## 設定

[サーバー設定例](../configs/server.example.yaml)の `dns_inventory` と `google_sheets` に、スプレッドシートID、シートごとの範囲、サービスアカウントのJSONファイル、出力先を設定する。対象スプレッドシートはサービスアカウントに閲覧権限だけを付ける。JSONファイルを0600で取得用ユーザーだけが読めるようにする。取得コマンドは読み取り専用のOAuthスコープを使い、サービスアカウントのJSONや応答本文はエラーに表示しない。[サービスアカウントの認証方式](https://developers.google.com/identity/protocols/oauth2/service-account)

範囲は `'Example inventory'!A2:G256` のように、対象のシート名と見出し行を除いたセル範囲を指定する。[DNS台帳の記入例](dns-inventory.md#台帳の記入例)

```sh
certpouch dns fetch --config /etc/certpouch/server.yaml
```

対象のシートID、指定したすべての範囲と順番、列数、台帳の内容を確認してからLinux上でキャッシュを置き換える。取得障害、範囲の欠落、不正な台帳、すべての名前が空のスナップショットを受け取った場合は、以前のキャッシュと最終成功時刻を維持する。キャッシュは検証したAPI応答をJSONに整形して保存する。同じ内容なら台帳本体を変えず、最終成功時刻とキャッシュのSHA-256を `cache.json.freshness.json` に記録する。出力は更新の有無と名前の件数だけを表示する。

```sh
certpouch dns fetch --status --config /etc/certpouch/server.yaml
```

`--status` はGoogleへ接続せず、サービスアカウントも読まず、最終成功時刻とキャッシュの状態をJSONで返す。`dns_inventory.max_age: "45m"` のように設定すると期限も確認できる。状態は `verified`、`stale`、`untracked`、`missing`、`invalid` のいずれかで、検証済みの `verified` を終了コード0、他の状態を1、設定エラーを2とする。

キャッシュの親ディレクトリは事前に作成し、取得ユーザーに通過・書き込み権限を与える。既存のキャッシュを使う場合は、取得ユーザーの所有にする。配布サーバーが別ユーザーなら、取得用と配布用に割り当てた共有グループで読めるよう、親ディレクトリの通過権限とファイルの0640を設定する。新規作成時の既定値は0600。既存ファイルのモードとグループは更新時に引き継ぐ。キャッシュの書き手は一つにする。異常終了で `cache.json.fetch-lock` が残った場合は実行中の取得処理がないことを確認してから空のロックディレクトリを取り除く。

## 定期取得

[取得用service](../configs/systemd/certpouch-sheets-fetch.service)と[timer](../configs/systemd/certpouch-sheets-fetch.timer)は15分ごとに取得する例。専用の `certpouch-sheets` ユーザーを用意し、キャッシュの出力先と権限を合わせる。手動で取得成功を確認してからunitを配置する。

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now certpouch-sheets-fetch.timer
```

新しい台帳からゾーンを作るには [DNSゾーン生成と適用](dns-sync.md)を参照。配布認可に新しい台帳を自動反映する場合は、[配布サーバーの台帳設定](dns-inventory.md)で `server.inventory_refresh` を設定する。取得したキャッシュを基に候補を確認してからDNSの変更を適用する。
