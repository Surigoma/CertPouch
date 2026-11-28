# Linuxへのインストールと削除

リリースアーカイブには、Linux用バイナリ、`install.sh`、`uninstall.sh` が入っています。

- インストーラーはバイナリだけを配置します。
- 設定、資格情報、証明書、状態、サービスアカウント、systemd unitは変更しません。
- 初期設定とunitの選択は役割ごとに異なるため、インストール後に導入手順に従って行います。

## インストール

アーカイブのSHA-256を確認してから展開し、展開したディレクトリで実行します。

```sh
sudo ./install.sh
```

既定の配置先は次のとおりです。

| 配置するもの | 場所 |
| --- | --- |
| バイナリ | `/usr/local/bin/certpouch` |
| 管理情報とアンインストーラー | `/usr/local/share/certpouch/` |

別の階層へ配置する場合は、絶対パスを指定します。

```sh
sudo ./install.sh --prefix /opt/certpouch
```

- インストーラーが管理していない同名のファイルは上書きしません。既存のファイルを明示的に置き換える場合だけ `--replace` を使います。
- 管理済みのバイナリに対する再実行は、更新として扱います。

インストール後は、役割に応じて次へ進みます。

| 役割 | 手順 |
| --- | --- |
| 配布サーバー | [サーバーへの証明書登録](server-publication.md) |
| Pullクライアント | [クライアントへの配置](client-deployment.md) |
| DNS管理 | [DNSゾーン生成と適用](dns-sync.md) |
| Sheets取得 | [Sheetsからの取得](sheets-fetch.md) |

## GitHub Releasesから単体バイナリを更新する

Linux amd64・arm64では、`update` に対応した安定版リリースから配置した単体バイナリを、その場所で更新できます。ホームディレクトリに置いた場合の例です。

```sh
~/certpouch/certpouch --version
~/certpouch/certpouch update --check
~/certpouch/certpouch update
```

- 公開済みの安定版が現在より新しければ、対応するアーカイブをHTTPSで取得し、`SHA256SUMS` と実行ファイルの版を照合してから切り替えます。
- GitHubトークンは不要です。
- 実行ファイルと親ディレクトリは更新するユーザーの所有とし、他のユーザーからの書き込みを禁止します。
- `install.sh` で配置したバイナリは、引き続き同梱のインストーラーで更新します。

### 日次で自動更新する

`configs/systemd/certpouch-update.service` と `certpouch-update.timer` を `~/.config/systemd/user/` にコピーします。バイナリを別の場所に置いた場合は、serviceの `ExecStart` を実際のパスに合わせます。

```sh
systemctl --user daemon-reload
systemctl --user enable --now certpouch-update.timer
systemctl --user list-timers certpouch-update.timer
```

- タイマーは、ユーザーのsystemdマネージャーが稼働している間に動作します。
- 更新したバイナリは次回の起動から使われます。設定、キャッシュ、証明書には触れず、稼働中のサービスも再起動しません。
- ログは `journalctl --user -u certpouch-update.service` で確認できます。

### 1つ前のバイナリに戻す

タイマーを停止し、更新サービスの実行が終わっていることを確認してから戻します。

```sh
systemctl --user disable --now certpouch-update.timer
mv -T -- ~/certpouch/certpouch.previous ~/certpouch/certpouch
```

## アンインストール

既定の配置を削除します。

```sh
sudo /usr/local/share/certpouch/uninstall.sh
```

`--prefix` を使ってインストールした場合は、削除時にも同じ値を指定します。

```sh
sudo /opt/certpouch/share/certpouch/uninstall.sh --prefix /opt/certpouch
```

- 削除前に、バイナリのSHA-256を管理情報と照合します。インストール後にバイナリが変更されている場合は、削除を拒否します。
- 内容を確認したうえで削除する場合だけ `--force` を指定します。

アンインストーラーは次のものを削除しません。

- 設定、資格情報、証明書
- 公開・配置世代、DNSキャッシュ
- systemd unit、サービスアカウント

不要なunitは、停止・無効化してから管理者が削除します。運用データを消す場合は、バックアップと対象のパスを確認したうえで個別に削除してください。
