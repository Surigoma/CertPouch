# 既存のプライベートCAによる証明書更新

`cert renew` は、既存CAで未発行の証明書を発行し、期限が近い証明書を更新します。検証済みの新しい世代は、配布ストアに登録します。発行条件は `server.yaml` の `certificates` 節に書きます。

## 発行対象の選び方

| 構成 | 対象 | 台帳の更新方法 |
| --- | --- | --- |
| `certificates.targets` を省略 | 台帳上の各名前と別名に、独立した証明書を発行する | `pipeline --watch` に統一する（下記） |
| `certificates.targets` に列挙 | 列挙した名前だけ | `dns fetch` や取得タイマーも使える |

### 台帳上のすべての名前を発行する

[サーバー設定例](../configs/server.example.yaml)のように、`certificates.issuer` に既存CAの証明書・秘密鍵・中間チェーンを、`validity: "720h"` と `renew_before: "168h"` を指定し、`certificates.targets` を省略します。

- 台帳から削除した名前は更新対象から外れ、配布側の定期読み直し（`distribution.refresh_interval`、既定5秒）で取得対象からも外れます。
- 台帳キャッシュへの書き込みは、NSDへの反映を確認する `pipeline --watch` に統一します。
- 台帳だけを更新する `dns fetch` や独立した取得タイマーを同じキャッシュに向けると、DNSへの反映前に配布側が新しい台帳を読めてしまうため、併用しないでください。

### 名前を固定する

`certificates.targets` に名前を列挙します。

- 要素ごとに `expected_dns_sans`、`validity`、`renew_before` を指定できます。省略した期間は共通値を使います。
- 信頼するルートには `certificates.trust_ca_file` を使います。

## 更新を実行する

```sh
certpouch cert renew --dry-run --config /etc/certpouch/server.yaml
certpouch cert renew --config /etc/certpouch/server.yaml
```

プレビューの表示:

| 表示 | 条件 |
| --- | --- |
| `issue` | 未発行 |
| `renew` | 残存期間が `renew_before` 以下 |
| `unchanged` | それ以外 |

- プレビューはCA秘密鍵を読み込みません。
- 有効期間は `2160h` のようにGoの期間表記で指定します。

## CA鍵とチェーン

**署名に使うもの**

- 既存のRSA CA証明書と、対応するPEMの秘密鍵を使います。
- 中間CAを使う場合は、`certificates.issuer.chain_file` に署名CAを先頭として上位の中間CAを並べます。ルートCAは含めません。
- ルートCAが直接署名する構成では、`chain_file` を省略します。

**暗号化PEMのCA鍵**

- LinuxにOpenSSLを用意し、パスフレーズを書いた0600の1行のファイルを `certificates.issuer.passphrase_file` に指定します。
- パスフレーズは専用のファイルディスクリプターからOpenSSLに渡し、復号したCA鍵はプロセスのメモリ上だけで扱います。
- HSMなどの外部署名器との連携は未対応です。

**CA鍵の保護**

- CA鍵は配布ストアの外で0600に保ち、署名する運用ユーザーだけが読めるようにします。
- 配布サーバーには、CA鍵へのアクセス権を与えません。

## 発行と公開の動作

- 更新ごとに、新しいRSA 2048ビットのリーフ秘密鍵を生成し、DNS/IP SANとServer Authentication用途を設定します。
- 信頼チェーンの有効期間を超える発行は拒否します。
- 新しい世代は、検証に通った後にだけ公開します。
- 更新を判断した後に別の操作が公開世代を変更した場合は、その世代を上書きせずに失敗を返します。
- 複数の証明書は順番に処理します。途中で失敗しても、先に成功した更新は残ります。
- 配布サーバーは `distribution.refresh_interval` ごとにストアを読み直すため、次回の読み込みで配布できるようになります。

## systemdでの定期更新

[更新用service](../configs/systemd/certpouch-renew.service)と[timer](../configs/systemd/certpouch-renew.timer)は、更新を1日1回実行する例です。

- 署名処理はrootで実行し、CA鍵とパスフレーズはrootだけが読めるようにします。
- 配布ストアは `certpouch` ユーザーの所有とします。発行・公開したファイルは、ストアの所有者に合わせて保存されます。
- 設定例と異なるストアを使う場合は、serviceの `ReadWritePaths` を変更します。

手動でプレビュー、更新、配布への反映を確認してから、unitを配置します。

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now certpouch-renew.timer
systemctl list-timers certpouch-renew.timer
```

- 実行結果は `journalctl -u certpouch-renew.service` で確認します。
- 署名を止めるにはタイマーを停止します。実行中のserviceがある場合は、終了を確認します。

## 公開証明書の期限監視

```sh
certpouch cert status --config /etc/certpouch/server.yaml --expires-within 168h
```

公開ストアのリーフ証明書から、世代、有効期間、フィンガープリント、状態をJSONで出力します。閾値の既定値は30日です。

| 終了コード | 条件 |
| --- | --- |
| 0 | すべて正常 |
| 1 | 指定期間内に期限が来る証明書、未公開、期限切れ、有効期間前、読み取り・解析エラーが1件でもある |
| 2 | 設定が不正 |

このコマンドは、サーバーへの接続や秘密鍵の読み込みを行わず、ディスク上の公開証明書の期限だけを確認します。実際にHTTPSで取得できる世代と、サービスが提示する証明書は、配布APIとクライアント側のTLS確認で別途検証してください。
