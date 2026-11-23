# サーバーへの証明書登録

Linux上で、既存CAが発行したリーフ証明書と対応する秘密鍵を `cert publish` で登録する。対象の論理名と検証するSAN・発行者を、事前に[サーバー設定](../configs/server.example.yaml)の `certificates` に設定する。`init server` を使った場合は、以下の `--config` とunitの `ExecStart` に生成した `server.yaml` のパスを指定する。[初回導入](quickstart.md)

```sh
certpouch cert publish --config /etc/certpouch/server.yaml \
  --name web.example.internal \
  --certificate /secure/drop/leaf.pem \
  --chain /secure/drop/intermediates.pem \
  --private-key /secure/drop/private-key.pem
```

直接ルートCAが署名した証明書では `--chain` を省略できる。中間CAがある場合は直接発行者から順に格納し、ルートCAを含めない。登録元の秘密鍵とサーバーの保存領域は所有者だけがアクセスできるようにする。CAの署名鍵は登録対象に含めない。

登録処理は証明書と鍵、SAN、発行者、有効期限、チェーンを検証してから新しい世代を書き込み、`current` を切り替える。検証に失敗した場合は現行世代を維持する。成功時は証明書名と新しい世代IDを表示する。

`server.publication_refresh` を設定すると、配布サーバーがストアを定期的に読み直す。例の `1m` では1分ごとに確認し、検証が通った在庫へ切り替える。失敗した場合は最後に検証済みの在庫を維持し、エラーをログに記録する。次にストアの検証が成功するまで `/api/v1/health` は503を返す。定期確認を省略すると起動時のみストアを確認するため、登録後にサーバーを再起動する。アクセス制御、信頼CA、サーバー自身のTLS鍵など、設定を変えた場合は再起動が必要。

古い世代は保存される。登録中の異常終了で `.publish-lock` が残った場合は、登録処理が終了していることを確認し、`current` の参照先と登録済みファイルを確認してからロックを取り除く。

## systemdで配布サーバーを動かす場合

[サーバー用unit例](../configs/systemd/certpouch-server.service)は、`/usr/local/bin/certpouch` と `/etc/certpouch/server.yaml` を使い、専用の `certpouch` ユーザーとグループで実行する。利用前に実行ユーザーを用意し、設定とCAを読み取れるようにする。サーバーTLS秘密鍵、トークンハッシュ、0700の証明書ストアは専用ユーザーのみがアクセスできるようにする。

起動前に、実行ユーザーで `certpouch server --check-config --config /etc/certpouch/server.yaml` を実行する。設定、TLS証明書と鍵の組み合わせ、公開世代を読み取って検証し、ポートは開かない。

unitを `/etc/systemd/system/` に置いたあと、`sudo systemctl daemon-reload` と `sudo systemctl enable --now certpouch-server.service` で起動する。登録は別途実行する。管理者が実行した場合、新しいディレクトリとファイルはストアの所有者・グループに合わせる。以前に別の所有者で作成された証明書ディレクトリがある場合は、作業前にストアの所有者と一致させる。unitは配布サーバーからのファイルシステム書き込みを制限する。設定例は非特権ポートを使用する。
