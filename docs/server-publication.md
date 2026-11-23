# サーバーへの証明書登録

Linux上で、既存CAが発行したリーフ証明書と対応する秘密鍵を `cert publish` で登録する。対象の論理名と検証するSAN・発行者を、事前に[サーバー設定](../configs/server.example.yaml)の `certificates` に設定する。

```sh
certpouch cert publish --config /etc/certpouch/server.yaml \
  --name web.example.internal \
  --certificate /secure/drop/leaf.pem \
  --chain /secure/drop/intermediates.pem \
  --private-key /secure/drop/private-key.pem
```

直接ルートCAが署名した証明書では `--chain` を省略できる。中間CAがある場合は直接発行者から順に格納し、ルートCAを含めない。登録元の秘密鍵とサーバーの保存領域は所有者だけがアクセスできるようにする。CAの署名鍵は登録対象に含めない。

登録処理は証明書と鍵、SAN、発行者、有効期限、チェーンを検証してから新しい世代を書き込み、`current` を切り替える。検証に失敗した場合は現行世代を維持する。成功時は証明書名と新しい世代IDを表示する。配布サーバーは起動時に公開対象を読み込むため、登録後に再起動して在庫を確認する。

古い世代は保存される。登録中の異常終了で `.publish-lock` が残った場合は、登録処理が終了していることを確認し、`current` の参照先と登録済みファイルを確認してからロックを取り除く。
