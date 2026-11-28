# サーバーへの証明書登録

既存CAが発行したリーフ証明書と秘密鍵を、Linux上の配布サーバーへ `cert publish` で登録する手順です。

## 前提

- 対象の論理名と、検証するSAN・発行者を、[サーバー設定](../configs/server.example.yaml)の `certificates.targets` に設定しておきます。
- CA秘密鍵を持たない配布サーバーでは、`certificates.issuer.private_key_file` を省略します。
- `configure server` を使った場合は、以下の `--config` とunitの `ExecStart` に、生成した `server.yaml` のパスを指定します。[手動配布の手順](manual-distribution.md)

## 登録する

```sh
certpouch cert publish --config /etc/certpouch/server.yaml \
  --name web.example.internal \
  --certificate /secure/drop/leaf.pem \
  --chain /secure/drop/intermediates.pem \
  --private-key /secure/drop/private-key.pem
```

**チェーン**

- 中間CAがある場合は、直接の発行者から順に格納し、ルートCAは含めません。
- ルートCAが直接署名した証明書では、`--chain` を省略できます。

**保護**

- 登録元の秘密鍵とサーバーの保存領域は、所有者だけがアクセスできるようにします。
- CAの署名鍵は登録対象に含めません。

**登録の動作**

1. 証明書と鍵、SAN、発行者、有効期限、チェーンを検証します。
2. 新しい世代を書き込み、`current` を切り替えます。
3. 成功すると、証明書名と新しい世代IDを表示します。

検証に失敗した場合は、現行の世代を維持します。古い世代は削除されずに残ります。

## 配布への反映

- 配布サーバーは `distribution.refresh_interval`（既定5秒）ごとにストアを読み直し、検証に通った在庫へ切り替えます。
- 読み直しに失敗した場合は、最後に検証済みの在庫を維持し、エラーをログに記録します。次に検証が成功するまで、`/api/v1/health` は503を返します。
- アクセス制御、信頼CA、サーバー自身のTLS鍵など、設定を変えた場合は再起動が必要です。

登録中の異常終了で `.publish-lock` が残った場合:

1. 登録処理が終了していることを確認します。
2. `current` の参照先と、登録済みのファイルを確認します。
3. ロックを取り除きます。

## systemdで配布サーバーを動かす

[サーバー用unit例](../configs/systemd/certpouch-server.service)は、`/usr/local/bin/certpouch` と `/etc/certpouch/server.yaml` を使い、専用の `certpouch` ユーザーとグループで実行します。

**権限を用意する**

- 実行ユーザーを用意し、設定とCAを読み取れるようにします。
- サーバーのTLS秘密鍵、トークンハッシュ、0700の証明書ストアは、専用ユーザーだけがアクセスできるようにします。
- 管理者が登録した場合も、新しいディレクトリとファイルはストアの所有者・グループに合わせて作られます。以前に別の所有者で作られた証明書ディレクトリがある場合は、作業前にストアの所有者と一致させます。

**起動する**

1. 実行ユーザーで設定を検証します。設定、TLS証明書と鍵の組み合わせ、公開世代を読み取って検証し、ポートは開きません。

   ```sh
   sudo -u certpouch certpouch server --check-config --config /etc/certpouch/server.yaml
   ```

2. unitを `/etc/systemd/system/` に置き、起動します。

   ```sh
   sudo systemctl daemon-reload
   sudo systemctl enable --now certpouch-server.service
   ```

3. 証明書の登録は、上記の `cert publish` で別途行います。

unitは、配布サーバーからのファイルシステムへの書き込みを制限します。設定例は非特権ポートを使います。
