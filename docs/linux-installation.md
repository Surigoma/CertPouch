# Linuxへのインストールと削除

リリースアーカイブにはLinux用バイナリと `install.sh`、`uninstall.sh` を収録する。インストーラーはバイナリだけを配置し、設定、資格情報、証明書、状態、サービスアカウント、systemd unitを変更しない。初期設定とunitの選択は役割ごとに異なるため、導入手順に従って別に行う。

## インストール

アーカイブのSHA-256を確認して展開し、展開したディレクトリで実行する。

```sh
sudo ./install.sh
```

既定ではバイナリを `/usr/local/bin/certpouch`、管理情報とアンインストーラーを `/usr/local/share/certpouch/` に配置する。異なる階層へ配置する場合は絶対パスを指定する。

```sh
sudo ./install.sh --prefix /opt/certpouch
```

インストーラーが管理していない同名ファイルは上書きしない。既存ファイルを明示的に置き換える場合だけ `--replace` を使う。管理済みのバイナリへの再実行は更新として扱う。

インストール後、用途に応じて次を行う。

- 配布サーバー: [サーバーへの証明書登録](server-publication.md)
- Pullクライアント: [クライアントへの配置](client-deployment.md)
- DNS管理: [DNSゾーン生成と適用](dns-sync.md)
- Sheets取得: [Sheetsからの取得](sheets-fetch.md)

## アンインストール

既定の配置を削除する。

```sh
sudo /usr/local/share/certpouch/uninstall.sh
```

`--prefix` を使ってインストールした場合は、削除時にも同じ値を指定する。

```sh
sudo /opt/certpouch/share/certpouch/uninstall.sh --prefix /opt/certpouch
```

削除前にバイナリのSHA-256を管理情報と照合する。インストール後にバイナリが変更されている場合は削除を拒否する。内容を確認したうえで削除する場合だけ `--force` を指定する。

設定、資格情報、証明書、公開・配置世代、DNSキャッシュ、systemd unit、サービスアカウントは削除しない。不要なunitは停止・無効化してから管理者が別途削除する。運用データを消す場合はバックアップと対象パスを確認して個別に行う。
