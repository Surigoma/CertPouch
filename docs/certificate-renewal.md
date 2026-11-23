# 既存のプライベートCAによる証明書更新

`cert renew` は、既存のプライベートCAで期限が近い証明書を更新し、検証済みの新しい世代を配布ストアに登録する。対象は[更新設定例](../configs/renewal.example.yaml)のプロファイルで明示する。対象名、SAN、期待する発行者、信頼するルートはサーバー設定の `certificates` と `storage.trust_ca_file` を使う。

```sh
certpouch cert renew --dry-run --config /etc/certpouch/server.yaml \
  --renewal-config /etc/certpouch-issuer/renewal.yaml
certpouch cert renew --config /etc/certpouch/server.yaml \
  --renewal-config /etc/certpouch-issuer/renewal.yaml
```

プレビューでは未発行なら `issue`、残存期間が `renew_before` 以下なら `renew`、それ以外は `unchanged` を表示する。プレビューはCA秘密鍵を読み込まない。有効期間は `2160h` のようにGoの期間表記で指定する。

署名には既存のRSA CA証明書と対応するPEMの秘密鍵を使う。中間CAを使う場合は `issuer_chain` に署名CAを先頭として上位中間CAを並べる。ルートCAを含めない。直接ルートCAが署名する構成では `issuer_chain` を省略する。

暗号化PEMの場合は、LinuxにOpenSSLを用意し、0600の1行のファイルを `issuer_passphrase_file` に指定する。パスフレーズは専用のファイルディスクリプターからOpenSSLに渡し、復号済みのCA鍵はプロセスのメモリ上でだけ扱う。HSMなどの外部署名器との連携は未対応。

CA鍵は配布ストアの外で0600に保ち、署名する運用ユーザーにだけ読み取りを許可する。配布サーバーにはCA鍵へのアクセス権を与えない。更新ごとに新しいRSA 2048ビットのリーフ秘密鍵を生成し、DNS/IP SANとServer Authentication用途を設定する。信頼チェーンの有効期間を超える発行は拒否する。

新しい世代は検証後にだけ公開する。更新判断のあと別操作が公開世代を変更した場合は、その世代を上書きせず失敗を返す。複数のプロファイルは順番に処理するため、途中で失敗した場合も先に成功した更新は残る。`server.publication_refresh` を設定していれば次回の読み込みで配布できる。省略した場合はサーバーを再起動する。

## systemdでの定期更新

[更新用service](../configs/systemd/certpouch-renew.service)と[timer](../configs/systemd/certpouch-renew.timer)は、更新を1日1回実行する例。署名処理はrootで実行し、CA鍵とパスフレーズはrootのみが読めるようにする。配布ストアは `certpouch` ユーザーの所有とし、発行・公開したファイルはストアの所有者に合わせて保存する。設定例と異なるストアを使う場合はserviceの `ReadWritePaths` を変更する。

手動でプレビューと更新、配布への反映を確認してからunitを配置する。

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now certpouch-renew.timer
systemctl list-timers certpouch-renew.timer
```

実行結果は `journalctl -u certpouch-renew.service` で確認する。署名の停止にはタイマーを停止し、稼働中のserviceがある場合は終了を確認する。
