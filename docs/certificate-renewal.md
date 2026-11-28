# 既存のプライベートCAによる証明書更新

発行条件は `server.yaml` の `certificates` 節に書く。[サーバー設定例](../configs/server.example.yaml)のように `certificates.issuer` に既存CAの証明書・秘密鍵・中間チェーン、`validity: "720h"` と `renew_before: "168h"` を指定し、`certificates.targets` を省略すると、台帳上の各名前と別名に独立した証明書を発行する。削除済みの名前は更新対象から外れ、配布側の定期読み直し（`distribution.refresh_interval`、既定5秒）で取得対象から外れる。

この運用では、台帳キャッシュへの書き込みはNSD反映を確認する `pipeline --watch` に統一する。台帳だけを更新する `dns fetch` や独立した取得タイマーを同じキャッシュに向けると、DNS反映前に配布側が新しい台帳を読めるため、併用しない。

`cert renew` は、既存CAで未発行の証明書を発行し、期限が近い証明書を更新する。検証済みの新世代は配布ストアに登録する。名前を固定する場合は `certificates.targets` に列挙する。要素ごとに `expected_dns_sans`、`validity`、`renew_before` を指定でき、省略した期間は共通値を使う。信頼するルートは `certificates.trust_ca_file` を使う。

```sh
certpouch cert renew --dry-run --config /etc/certpouch/server.yaml
certpouch cert renew --config /etc/certpouch/server.yaml
```

プレビューでは未発行なら `issue`、残存期間が `renew_before` 以下なら `renew`、それ以外は `unchanged` を表示する。プレビューはCA秘密鍵を読み込まない。有効期間は `2160h` のようにGoの期間表記で指定する。

署名には既存のRSA CA証明書と対応するPEMの秘密鍵を使う。中間CAを使う場合は `certificates.issuer.chain_file` に署名CAを先頭として上位中間CAを並べる。ルートCAを含めない。直接ルートCAが署名する構成では `chain_file` を省略する。

暗号化PEMの場合は、LinuxにOpenSSLを用意し、0600の1行のファイルを `certificates.issuer.passphrase_file` に指定する。パスフレーズは専用のファイルディスクリプターからOpenSSLに渡し、復号済みのCA鍵はプロセスのメモリ上でだけ扱う。HSMなどの外部署名器との連携は未対応。

CA鍵は配布ストアの外で0600に保ち、署名する運用ユーザーにだけ読み取りを許可する。配布サーバーにはCA鍵へのアクセス権を与えない。更新ごとに新しいRSA 2048ビットのリーフ秘密鍵を生成し、DNS/IP SANとServer Authentication用途を設定する。信頼チェーンの有効期間を超える発行は拒否する。

新しい世代は検証後にだけ公開する。更新判断のあと別操作が公開世代を変更した場合は、その世代を上書きせず失敗を返す。複数の証明書は順番に処理するため、途中で失敗した場合も先に成功した更新は残る。配布サーバーは `distribution.refresh_interval` ごとにストアを読み直すため、次回の読み込みで配布できる。

## systemdでの定期更新

[更新用service](../configs/systemd/certpouch-renew.service)と[timer](../configs/systemd/certpouch-renew.timer)は、更新を1日1回実行する例。署名処理はrootで実行し、CA鍵とパスフレーズはrootのみが読めるようにする。配布ストアは `certpouch` ユーザーの所有とし、発行・公開したファイルはストアの所有者に合わせて保存する。設定例と異なるストアを使う場合はserviceの `ReadWritePaths` を変更する。

手動でプレビューと更新、配布への反映を確認してからunitを配置する。

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now certpouch-renew.timer
systemctl list-timers certpouch-renew.timer
```

実行結果は `journalctl -u certpouch-renew.service` で確認する。署名の停止にはタイマーを停止し、稼働中のserviceがある場合は終了を確認する。

## 公開証明書の期限監視

```sh
certpouch cert status --config /etc/certpouch/server.yaml --expires-within 168h
```

公開ストアのリーフ証明書から、世代、有効期間、フィンガープリント、状態をJSONで出力する。指定した期間以内に期限が来る証明書や、未公開、期限切れ、有効期間前、読み取り・解析エラーが一件でもあれば終了コード1を返す。すべて正常なら0、設定が不正なら2。閾値の既定値は30日。

このコマンドはサーバーへの接続や秘密鍵の読み込みを行わず、ディスク上の公開証明書の期限を確認する。実際にHTTPSで取得できる世代とサービスが提示する証明書は、配布APIとクライアント側のTLS確認で別途検証する。
