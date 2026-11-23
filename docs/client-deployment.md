# Linuxでの証明書配置

このページのコマンドは手動設定例の `/etc/certpouch/client.yaml` を使う。`init client` で生成したYAMLを使う場合は、Pull、status、systemdユニットの `--config` を生成先に揃える。[初回導入](quickstart.md)

[クライアント設定例](../configs/client.example.yaml)をもとに、配布サーバーのTLS用CA、証明書の発行CA、クライアントトークン、配置先、systemdのユニットと操作を設定する。配置先は信頼できる管理者が事前に作成し、実行ユーザーに書き込みを許可する。サービスには配置先と `versions` の通過権限を与える。秘密鍵は既定で0600。サービス専用グループで共有する場合は `group` と `private_key_mode: "0640"` を指定する。

すでに作成済みの `versions` ディレクトリを再利用する場合は、その所有グループと通過権限を確認する。Pullは新しく作る `versions` に設定済みのグループと0750を適用する。

```sh
certpouch pull --dry-run --config /etc/certpouch/client.yaml
certpouch pull --config /etc/certpouch/client.yaml
certpouch status --config /etc/certpouch/client.yaml
```

サービスは配置先の `current/fullchain.pem` と `current/private-key.pem` を参照する。初回導入前に、既存サービス設定と証明書を退避する。初回配置で切り戻し先がない場合に備え、元のサービス設定と参照先へ戻せるようにする。

通常のPullは、検証済みのファイルを世代ディレクトリに保存し、`current` を切り替えてから、証明書ごとに設定した `service` または `command` を実行する。両方の指定は設定エラーになる。systemdの稼働状態やコマンドの終了コードに加え、利用サービスが新しい証明書を実際に配信することも確認する。

実際に配信された証明書まで自動で確認する場合は、証明書ごとの `service.verify_tls` を使う。プロキシ経由ではなく、対象インスタンスのTLSポートを指定する。例の `server_name` は証明書のSANに含める。

```yaml
service:
  unit: "keycloak.service"
  action: "restart"
  verify_tls:
    address: "127.0.0.1:8443"
    server_name: "sso.example.internal"
    timeout: "60s"
```

Pullは `validation.trust_ca_file` と指定名でTLSを検証し、相手が返したリーフと現在の世代のSHA-256フィンガープリントを照合する。設定した操作の完了後、`timeout` の範囲で照合を繰り返し、一致しなければ配置失敗として切り戻す。既定値は15秒で、1秒から2分まで指定できる。旧世代の再反映と異常終了後の復旧でも同じ設定を使う。

## Dockerなどをコマンドで反映する

対象の証明書から `service` を外し、代わりに以下の `command` を指定する。

```yaml
command:
  args: ["/usr/bin/docker", "restart", "web"]
  timeout: "1m"
  verify_tls:
    address: "127.0.0.1:443"
    server_name: "web.example.internal"
```

`args` の先頭が実行ファイルで、残りの要素はそれぞれ1個の引数となる。パイプ、リダイレクト、変数展開などのシェル構文は解釈しない。コマンドはPullの実行ユーザーで動くため、例ではDockerへのアクセス権限が必要。実行ファイルには絶対パスを推奨する。コマンドの `timeout` は既定30秒で、正の期間を指定できる。非ゼロ終了またはタイムアウトは反映失敗となる。旧世代への切り戻し後も同じコマンドを実行する。標準出力と標準エラーはPullの出力に含めない。

コンテナで使う場合は、ホストの配置先ディレクトリ全体をコンテナに読み取り専用でマウントし、コンテナ側で `current/fullchain.pem` と `current/private-key.pem` を参照する。個別の証明書ファイルをマウントすると、古い世代を参照し続ける場合がある。実行ユーザーとコンテナ側ユーザーの読取権限を確認する。コマンドの終了と実際のTLS配信を照合するには `command.verify_tls` を指定する。

Docker Composeを使う場合は `args: ["/usr/bin/docker", "compose", "-f", "/srv/web/compose.yaml", "restart", "web"]` のように、Composeファイルを絶対パスで指定できる。

## Keycloakの設定と試験

Keycloakでは `https-certificate-file` に `current/fullchain.pem`、`https-certificate-key-file` に `current/private-key.pem` を指定できる。例はサービス再起動で反映する構成。Keycloakの定期的な証明書再読み込みを待つ運用は、Pull直後の確認とは分けて検証する。[Keycloak公式のTLS設定](https://www.keycloak.org/server/enabletls)

Linuxでは、展開した検証専用のKeycloakと対応するJavaを使い、初回配置・更新・切り戻しを再現できる。テストはループバックとインメモリDBを使用し、Keycloakを起動する。

```sh
CERTPOUCH_TEST_KEYCLOAK_HOME=/path/to/disposable/keycloak \
  go test ./cmd/certpouch -run '^TestKeycloakCertificateDeployment$' -v -count=1
```

隔離したsystemd環境で `CERTPOUCH_TEST_SYSTEMD=1` も指定すると、一時サービスを作成し、Pullと同じサービス再起動とTLS照合を使って検証する。実行ユーザーには一時サービスを作成・操作できる権限が必要。

同じ環境変数を指定して `TestLinuxPublicationAndPullWorkflow` を実行すると、テストCAでの証明書公開から、配布APIを使う初回Pull、Keycloakの再起動失敗後の切り戻し、更新の再試行まで確認できる。失敗は一時ユニットの起動前処理で一度だけ発生させる。

`CERTPOUCH_TEST_SYSTEMD=1` だけを指定して同テストを実行すると、軽量なTLSサービスと `nobody` ユーザーのsystemdユニットを使用する。reload時に、サービスのグループが0640の秘密鍵と証明書を開けること、および秘密鍵と世代ディレクトリへの書き込みが拒否されることを確認する。

サービス操作に失敗すると、以前の世代がある場合は `current` を元に戻してサービスを再反映する。異常終了後の次回Pullは、サーバーへ接続する前に `.pull-lock/recovery` を調べる。記録と旧世代のファイルを確認できれば、旧世代へ切り戻してサービスを再反映してから同期を続ける。プレビューでは復旧処理は行わない。

旧世代がない初回失敗、記録の破損、世代の不一致、再反映失敗は手動復旧が必要。記録には旧世代と新世代への相対パスが1行ずつ入る。Pullが実行中でないことを確認し、`current`、サービスの状態、TLS応答を調べる。初回失敗では退避した設定を使う。再試行する場合は、失敗世代が使われていないことを確認して管理領域から退避する。正常なサービス状態を確認してから復旧記録と `.pull-lock` を取り除く。

配置先の `.pull-guard` はプロセス間の排他制御に使うため、削除しない。自動復旧を導入したバージョンに切り替える前に、旧バージョンのPullとタイマーを停止する。

複数の証明書は個別に処理する。途中で失敗した場合、それ以前に適用した更新は維持される。

`status` はサーバーに接続せず、配置世代、Subject、Issuer、SAN、有効期限、フィンガープリント、最終同期時刻をJSONで表示する。未配置、期限切れ、検証失敗、復旧記録などがあれば終了コード1を返す。`recovery_pending` は復旧記録がある状態を示すため、手動復旧の前にPullが終了していることを確認する。

配置先のロック取得後に失敗したPullは、前回の成功情報を保持したまま `last_error_category` を記録する。失敗記録がある場合、旧証明書が有効でもstatusは終了コード1を返す。次に成功したPullで失敗記録が消える。サーバーに接続できない場合など、配置先のロック取得前の失敗はPull自体の終了コードとログで監視する。

同じ公開世代のファイルが配布元と一致しない場合、Pullはその世代の上書きを拒否する。配布サーバーで検証済みの新世代を公開してから再試行する。現行世代を読み込めない場合は、Pullとタイマーを停止し、検証済みのバックアップを元の所有者・権限で復元する。statusとサービスのTLS応答を確認してからタイマーを再開する。バックアップはテスト用の配置先で先に復元を試し、秘密鍵に元と同等のアクセス制限を適用する。

成功した通常のPullは、変更がない場合も照合済みの世代・フィンガープリント・時刻を記録する。既定では配置先の `.sync.json` に保存する。`client.state_dir` を指定する場合は実行ユーザーが書き込めるディレクトリを事前に作成する。プレビューは記録を更新しない。

手動PullとTLS接続の確認後、[service例](../configs/systemd/certpouch-pull.service)と[timer例](../configs/systemd/certpouch-pull.timer)を `/etc/systemd/system/` に置くと定期実行できる。例はバイナリを `/usr/local/bin/certpouch` に置き、システムサービスの既定ユーザーで実行する想定。

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now certpouch-pull.timer
systemctl list-timers certpouch-pull.timer
```

例の実行周期は1時間で、ホスト間の同時実行を避けるため最大5分ずらす。失敗時は `journalctl -u certpouch-pull.service` と `certpouch status` で確認する。変更を止めるにはtimerを停止する。実行中のserviceがあれば、終了するまで待ってから配置状態を確認する。
