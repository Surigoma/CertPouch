# 利用シーン別の操作例

Linux上で `example.internal` を管理し、`web01.example.internal` の証明書を1台に配る例です。設定はサーバーの `/etc/certpouch/server.yaml` と、受け取り側の `/etc/certpouch/client.yaml` を使います。パス、名前、シート範囲は環境に合わせて置き換えてください。

## 1. SheetsにつないでDNS候補を見る

閲覧専用のサービスアカウントにシートを共有し、JSONを取得ユーザーだけが読めるようにします。キャッシュの親ディレクトリを用意します。シートのB列に `192.0.2`、C列に `10`、D列に `web01`、G列にも `web01` を入れると、`web01.pvt.example.internal` と `web01.example.internal` のAレコードを生成できます。G列は別名です。[台帳の記入例](dns-inventory.md)

新規の設定ディレクトリへ初期設定を作る例です。設定がすでにある場合は、同じ項目を既存の `pipeline.dns` に追加します。

```sh
certpouch configure dns --out /etc/certpouch \
  --cache /var/lib/certpouch/inventory.json \
  --zone example.internal --fallback-prefix 192.0.2 \
  --primary ns1.example.net \
  --spreadsheet-id REPLACE_WITH_SPREADSHEET_ID \
  --service-account /etc/certpouch-secrets/service-account.json \
  --range "'DNS'!A2:G256"
certpouch dns fetch --config /etc/certpouch/server.yaml
certpouch dns sync --config /etc/certpouch/server.yaml --dry-run
```

期待結果は正引き・逆引き候補が表示されることです。既存NSDへ移行する場合は、現行レコード、SOA、NS、TTLと候補を照合し、[初期登録の手順](dns-sync.md#nsdへの適用)へ進みます。

## 2. 台帳を更新してNSDに反映する

初期登録済みで、NSDが管理中の世代を配信していることが前提です。`pipeline.dns.deployment` に以下を設定し、候補ディレクトリを事前に作成します。固定の `zones.serial` と `zones.previous_serials` は外し、管理中の世代からserialを引き継ぎます。

```yaml
# server.yaml の pipeline.dns に追加
deployment:
  stage_dir: "/var/lib/certpouch/dns-staging"
  zone_dir: "/var/lib/certpouch/nsd-zones"
  nsd_config: "/etc/nsd/nsd.conf"
  dns_address: "127.0.0.1:53"
```

1. Sheetsを編集したら `certpouch pipeline --config /etc/certpouch/server.yaml` を実行し、出力された候補パスを控えます。
2. 下の `candidate` を実際のパスに置き換え、差分を確認します。

```sh
candidate=/var/lib/certpouch/dns-staging/certpouch-zones-12345
certpouch dns diff --zone-dir /var/lib/certpouch/nsd-zones --candidate "$candidate"
```

3. 意図した差分であれば、同じ候補を指定して反映します。

```sh
certpouch pipeline --config /etc/certpouch/server.yaml --apply "$candidate"
```

反映はNSDが返すSOA serialまで確認します。既存レコードの削除・置換があると、このコマンドは停止します。IP変更などで置換が意図どおりなら、差分を確認し、個別の `dns apply` に許容件数を指定します。[適用と切り戻し](dns-sync.md)

## 3. 既存CAで証明書を発行・公開する

[サーバー設定例](../configs/server.example.yaml)を使い、`storage.issuer_certificate` に共通の直接発行者を指定します。トップレベルの `certificates` に `name: web01.example.internal` を登録し、同じ名前を `pipeline.certificates.renewal.profiles` に追加します。通常は証明書名から期待DNS SANが決まります。追加のSANが必要なら、期待する集合を明記します。

発行対象は更新プロファイルで指定します。配布許可はクライアント認証と接続元認可で設定します。CA秘密鍵は発行ユーザーだけが読める場所に保ちます。[CA更新の設定](certificate-renewal.md)・[配布認可](dns-inventory.md)

```sh
certpouch cert renew --config /etc/certpouch/server.yaml --dry-run
```

新規発行なら `issue`、更新対象なら `renew`、そのまま使えるなら `unchanged` を確認します。対象が正しければ実行します。

```sh
certpouch cert renew --config /etc/certpouch/server.yaml
certpouch cert status --config /etc/certpouch/server.yaml
```

## 4. 受け取り側で証明書を配置する

[クライアント設定例](../configs/client.example.yaml)に、配布サーバーのHTTPS URLとTLS用CA、クライアントIDとトークンファイル、配布証明書の信頼CAと直接発行者、証明書名、配置先、反映するサービスを設定します。証明書名はサーバーの公開名と揃えます。

```sh
certpouch pull --config /etc/certpouch/client.yaml --dry-run
```

初回は `install`、更新時は `update`、同じ世代なら `unchanged` が目安です。予定を確認してから配置します。

```sh
certpouch pull --config /etc/certpouch/client.yaml
certpouch status --config /etc/certpouch/client.yaml
```

`status` で配置世代と最終同期を確認します。サービスへの反映権限と失敗時の復旧については[クライアント配置手順](client-deployment.md)を参照してください。
