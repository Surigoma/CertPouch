# 利用シーン別の操作例

Linux上で `example.internal` を管理し、`web01.example.internal` の証明書を1台に配る例です。

- サーバーの設定: `/etc/certpouch/server.yaml`
- 受け取り側の設定: `/etc/certpouch/client.yaml`

パス、名前、シート範囲は環境に合わせて置き換えてください。

| シーン | 実行する場所 |
| --- | --- |
| [1. SheetsにつないでDNS候補を見る](#1-sheetsにつないでdns候補を見る) | サーバー |
| [2. 台帳を更新してNSDに反映する](#2-台帳を更新してnsdに反映する) | サーバー |
| [3. 既存CAで証明書を発行して公開する](#3-既存caで証明書を発行して公開する) | サーバー |
| [4. 受け取り側で証明書を配置する](#4-受け取り側で証明書を配置する) | 取得ホスト |

## 1. SheetsにつないでDNS候補を見る

**準備**

- 閲覧専用のサービスアカウントにシートを共有し、JSONを取得ユーザーだけが読めるようにします。
- キャッシュの親ディレクトリを用意します。
- シートのB列に `192.0.2`、C列に `10`、D列に `web01`、G列（別名）にも `web01` を入れます。これで `web01.pvt.example.internal` と `web01.example.internal` のAレコードが生成されます。[台帳の記入例](dns-inventory.md#台帳の記入例)

**実行**

新規の設定ディレクトリへ初期設定を作る例です。設定がすでにある場合は、同じ項目を既存の `server.yaml` の `inventory` と `dns` に追加します。

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

**期待結果:** 正引き・逆引きの候補が表示されます。既存のNSDから移行する場合は、現行のレコード、SOA、NS、TTLを候補と照合してから、[初期登録の手順](dns-sync.md#nsdへの適用)へ進みます。

## 2. 台帳を更新してNSDに反映する

**前提**

- NSDへの初期登録が済み、NSDが管理中の世代を配信していること。
- この手動の流れは、`certificates.targets` で名前を固定した構成か、証明書を発行しない構成で使います。台帳上のすべての名前を発行する構成では、`pipeline --once` または `--watch` を使います。

**準備**

`dns.nsd` に次を設定し、候補ディレクトリを事前に作成します。固定の `dns.zones.serial` と `dns.zones.previous_serials` は外し、管理中の世代からserialを引き継ぎます。

```yaml
# server.yaml の dns に追加
  nsd:
    stage_dir: "/var/lib/certpouch/dns-staging"
    zone_dir: "/var/lib/certpouch/nsd-zones"
    config_file: "/etc/nsd/nsd.conf"
    dns_address: "127.0.0.1:53"
```

**実行**

1. Sheetsを編集したら、`certpouch pipeline --config /etc/certpouch/server.yaml` を実行し、出力された候補のパスを控えます。
2. `candidate` を実際のパスに置き換えて、差分を確認します。

   ```sh
   candidate=/var/lib/certpouch/dns-staging/certpouch-zones-12345
   certpouch dns diff --zone-dir /var/lib/certpouch/nsd-zones --candidate "$candidate"
   ```

3. 意図した差分であれば、同じ候補を指定して反映します。

   ```sh
   certpouch pipeline --config /etc/certpouch/server.yaml --apply "$candidate"
   ```

反映は、NSDが返すSOA serialまで確認します。IPの変更や削除も反映されるため、実行前に必ず差分を確認してください。[適用と切り戻し](dns-sync.md)

## 3. 既存CAで証明書を発行して公開する

**設定**

[サーバー設定例](../configs/server.example.yaml)の `certificates` 節に、次を指定します。

- `issuer`: 直接発行者の証明書・秘密鍵・中間チェーン
- `validity` と `renew_before`
- 名前を固定する場合は `targets` に `name: web01.example.internal`。省略すると台帳上のすべての名前が対象です。

通常は証明書名から期待DNS SANが決まります。追加のSANが必要なら、期待する集合を明記します。配布の許可は、クライアント認証と接続元認可で設定します。CA秘密鍵は、発行ユーザーだけが読める場所に置いてください。[CA更新の設定](certificate-renewal.md)・[配布認可](dns-inventory.md)

**実行**

1. プレビューで対象を確認します。

   ```sh
   certpouch cert renew --config /etc/certpouch/server.yaml --dry-run
   ```

   | 表示 | 意味 |
   | --- | --- |
   | `issue` | 新規発行 |
   | `renew` | 更新対象 |
   | `unchanged` | そのまま使える |

2. 対象が正しければ実行し、公開された証明書を確認します。

   ```sh
   certpouch cert renew --config /etc/certpouch/server.yaml
   certpouch cert status --config /etc/certpouch/server.yaml
   ```

## 4. 受け取り側で証明書を配置する

**設定**

[クライアント設定例](../configs/client.example.yaml)に、次を設定します。証明書名はサーバーの公開名と揃えます。

- 配布サーバーのHTTPS URLとTLS用CA
- クライアントIDとトークンファイル
- 配布証明書の信頼CAと直接発行者
- 証明書名、配置先、反映するサービス

**実行**

1. 予定を確認します。

   ```sh
   certpouch pull --config /etc/certpouch/client.yaml --dry-run
   ```

   | 表示 | 意味 |
   | --- | --- |
   | `install` | 初回の配置 |
   | `update` | 新しい世代への更新 |
   | `unchanged` | 同じ世代のまま |

2. 予定どおりであれば配置し、状態を確認します。

   ```sh
   certpouch pull --config /etc/certpouch/client.yaml
   certpouch status --config /etc/certpouch/client.yaml
   ```

`status` で配置世代と最終同期を確認します。サービスへの反映権限と、失敗時の復旧は[クライアント配置手順](client-deployment.md)を参照してください。
