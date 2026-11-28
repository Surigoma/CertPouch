# Linuxでの証明書配置

取得ホストで証明書を配置し、利用サービスへ反映する手順です。例では手動設定の `/etc/certpouch/client.yaml` を使います。`configure client` で生成したYAMLを使う場合は、Pull、status、systemdユニットの `--config` を生成先に揃えてください。[手動配布の手順](manual-distribution.md)

## 準備

[クライアント設定例](../configs/client.example.yaml)をもとに、次を設定します。

- 配布サーバーのTLS用CA
- 証明書の発行CA
- クライアントトークン
- 配置先
- 反映に使うsystemdユニットと操作

配置先と権限:

- 配置先は信頼できる管理者が事前に作成し、Pullの実行ユーザーに書き込みを許可します。
- サービスには、配置先と `versions` の通過権限を与えます。
- 秘密鍵は既定で0600です。サービス専用のグループで共有する場合は、`group` と `private_key_mode: "0640"` を指定します。
- Pullが新しく作る `versions` には、設定したグループと0750が適用されます。作成済みの `versions` を再利用する場合は、所有グループと通過権限を確認してください。

初回導入の前に、既存のサービス設定と証明書を退避します。初回配置で切り戻し先がない場合に備え、元の設定と参照先へ戻せるようにしておきます。

## Pullの実行

```sh
certpouch pull --dry-run --config /etc/certpouch/client.yaml
certpouch pull --config /etc/certpouch/client.yaml
certpouch status --config /etc/certpouch/client.yaml
```

通常のPullは次の順に動作します。

1. 検証済みのファイルを世代ディレクトリに保存し、`current` を切り替えます。
2. `service` または `command` を指定していれば実行します。両方を指定すると設定エラーです。
3. TLS照合を設定していれば、利用サービスが新しい証明書を実際に配信していることを確認します。

サービスには、配置先の `current/fullchain.pem` と `current/private-key.pem` を参照させます。

## 既存のサービスを切り替える

nginxなど、すでに別の証明書で動いているサービスをCertPouchの配置先へ切り替える手順です。サービスが `current/` を参照する前に、初回配置が必要です。

1. 現在サービスが配信している証明書の期限とSANを確認し、サービスの設定を退避します。
2. `service` を外した `client.yaml` で1回Pullし、`current/` を作ります。
3. サービスの証明書を `current/fullchain.pem`、秘密鍵を `current/private-key.pem` に変えます。nginxでは `ssl_certificate` と `ssl_certificate_key` です。
4. `nginx -t` などで設定を検証してからreloadし、配信されたリーフが `current/certificate.pem` と一致することを確認します。一致しなければ、退避した設定に戻してreloadします。
5. `service`（nginxでは `unit: nginx.service`、`action: reload`）と `verify_tls` を含む設定に戻し、定期実行を有効にします。

```sh
echo | openssl s_client -connect 127.0.0.1:443 -servername web.example.internal 2>/dev/null \
  | openssl x509 -noout -fingerprint -sha256
openssl x509 -in /etc/certpouch/certificates/web.example.internal/current/certificate.pem \
  -noout -fingerprint -sha256
```

> [!IMPORTANT]
> サービスのreloadやrestartには管理権限が必要です。ユーザーのsystemdタイマー（`systemctl --user`）で動かしたPullはnginxをreloadできないため、rootのシステムタイマーで実行します。その場合、バイナリと `client.yaml` はroot所有の場所（`/usr/local/bin/certpouch`、`/etc/certpouch/`）に置き、ホームディレクトリのバイナリをrootで実行しないでください。

- `client.yaml` は0600にします。権限が広いと `file permissions are too broad` で拒否されます。
- CertPouchの証明書は、SANが台帳上のFQDNだけになる場合があります。短縮名やIPで接続している利用者がいないか、事前に確認してください。[発行の動作](certificate-renewal.md#発行と公開の動作)

## systemdサービスで反映する

実際に配信された証明書まで自動で確認するには、証明書ごとに `service.verify_tls` を設定します。

```yaml
service:
  unit: "keycloak.service"
  action: "restart"
  verify_tls:
    address: "127.0.0.1:8443"
    server_name: "sso.example.internal"
    timeout: "60s"
```

- `address` には、プロキシではなく対象インスタンスのTLSポートを指定します。
- `server_name` は証明書のSANに含まれる名前にします。
- Pullは `validation.trust_ca_file` と `server_name` でTLSを検証し、相手が返したリーフと現在の世代のSHA-256フィンガープリントを照合します。
- 操作の完了後、`timeout` の範囲で照合を繰り返します。一致しなければ配置失敗として切り戻します。
- `timeout` の既定値は15秒で、1秒から2分まで指定できます。
- 旧世代の再反映と、異常終了後の復旧でも同じ設定を使います。

## Dockerなどをコマンドで反映する

対象の証明書から `service` を外し、代わりに `command` を指定します。

```yaml
command:
  args: ["/usr/bin/docker", "restart", "web"]
  timeout: "1m"
  verify_tls:
    address: "127.0.0.1:443"
    server_name: "web.example.internal"
```

**コマンドの実行方法**

- `args` の先頭が実行ファイルで、残りの要素がそれぞれ1個の引数になります。実行ファイルは絶対パスで指定することを推奨します。
- パイプ、リダイレクト、変数展開などのシェル構文は解釈しません。
- コマンドはPullの実行ユーザーで動きます。例ではDockerへのアクセス権限が必要です。
- `timeout` の既定は30秒で、正の期間を指定できます。
- 非ゼロ終了またはタイムアウトは反映失敗です。旧世代への切り戻し後も同じコマンドを実行します。
- コマンドの標準出力と標準エラーは、Pullの出力に含めません。
- コマンドの終了だけでなく実際のTLS配信まで確認するには、`command.verify_tls` を指定します。

**コンテナでの参照**

- ホストの配置先ディレクトリ全体を、コンテナに読み取り専用でマウントします。コンテナ側では `current/fullchain.pem` と `current/private-key.pem` を参照します。
- 個別の証明書ファイルをマウントすると、古い世代を参照し続ける場合があります。
- Pullの実行ユーザーとコンテナ側のユーザーの読み取り権限を確認してください。
- Docker Composeでは、`args: ["/usr/bin/docker", "compose", "-f", "/srv/web/compose.yaml", "restart", "web"]` のようにComposeファイルを絶対パスで指定できます。

## Keycloakの設定と試験

Keycloakでは、`https-certificate-file` に `current/fullchain.pem`、`https-certificate-key-file` に `current/private-key.pem` を指定できます。例はサービスの再起動で反映する構成です。Keycloakの定期的な証明書再読み込みを待つ運用は、Pull直後の確認とは分けて検証してください。[Keycloak公式のTLS設定](https://www.keycloak.org/server/enabletls)

Linuxでは、展開した検証専用のKeycloakと対応するJavaで、初回配置・更新・切り戻しを再現できます。テストはループバックとインメモリDBでKeycloakを起動します。

```sh
CERTPOUCH_TEST_KEYCLOAK_HOME=/path/to/disposable/keycloak \
  go test ./cmd/certpouch -run '^TestKeycloakCertificateDeployment$' -v -count=1
```

| 追加の環境変数・テスト | 確認できること |
| --- | --- |
| `CERTPOUCH_TEST_SYSTEMD=1` を追加 | 一時サービスを作成し、Pullと同じサービス再起動とTLS照合で検証する。実行ユーザーには一時サービスを作成・操作する権限が必要 |
| 同じ環境変数で `TestLinuxPublicationAndPullWorkflow` を実行 | テストCAでの公開、配布APIからの初回Pull、Keycloakの再起動失敗後の切り戻し、更新の再試行。失敗は一時ユニットの起動前処理で1回だけ発生させる |
| `CERTPOUCH_TEST_SYSTEMD=1` だけで同テストを実行 | 軽量なTLSサービスと `nobody` ユーザーのsystemdユニットで、reload時にサービスのグループが0640の秘密鍵と証明書を開けること、秘密鍵と世代ディレクトリへの書き込みが拒否されることを確認する |

## 複数の証明書と状態確認

**複数の証明書**

- 名前順に1件ずつ処理します。1件が失敗しても残りの処理を続け、適用済みの更新は維持します。
- 失敗した証明書は標準エラーに名前付きで表示し、最後に「N of M certificates failed」を出して終了コード1を返します。
- サーバーの在庫から消えた名前（削除した別名など）も、その証明書だけの失敗になります。不要になった証明書は `client.yaml` から外してください。

**status**

- サーバーに接続せず、配置世代、Subject、Issuer、SAN、有効期限、フィンガープリント、最終同期時刻をJSONで表示します。
- 未配置、期限切れ、検証失敗、復旧記録などがあれば終了コード1を返します。
- `recovery_pending` は復旧記録がある状態です。手動復旧の前に、Pullが終了していることを確認してください。

**同期記録**

- 成功した通常のPullは、変更がない場合も、照合済みの世代・フィンガープリント・時刻を記録します。プレビューは記録を更新しません。
- 記録は既定で配置先の `.sync.json` に保存します。`client.state_dir` を指定する場合は、実行ユーザーが書き込めるディレクトリを事前に作成します。
- 配置先のロック取得後に失敗したPullは、前回の成功情報を保持したまま `last_error_category` を記録します。失敗記録があると、旧証明書が有効でもstatusは終了コード1を返します。次に成功したPullで失敗記録は消えます。
- サーバーに接続できない場合など、ロック取得前の失敗は、Pull自体の終了コードとログで監視します。

## 失敗時の動作

- サービス操作に失敗すると、以前の世代がある場合は `current` を元に戻し、サービスを再反映します。
- 異常終了後の次回Pullは、サーバーへ接続する前に `.pull-lock/recovery` を調べます。記録と旧世代のファイルを確認できれば、旧世代へ切り戻してサービスを再反映してから同期を続けます。
- プレビューでは復旧処理を行いません。
- 同じ公開世代のファイルが配布元と一致しない場合、Pullはその世代の上書きを拒否します。配布サーバーで検証済みの新しい世代を公開してから再試行してください。

## 手動復旧

次の場合は手動復旧が必要です。

- 旧世代がない初回の失敗
- 復旧記録の破損
- 世代の不一致
- 再反映の失敗

復旧記録には、旧世代と新世代への相対パスが1行ずつ入っています。

1. Pullが実行中でないことを確認します。
2. `current`、サービスの状態、TLS応答を調べます。
3. 初回の失敗では、退避しておいた設定に戻します。
4. 再試行する場合は、失敗した世代が使われていないことを確認してから管理領域から退避します。
5. サービスが正常であることを確認してから、復旧記録と `.pull-lock` を取り除きます。

現行の世代を読み込めない場合:

1. Pullとタイマーを停止します。
2. 検証済みのバックアップを、元の所有者・権限で復元します。
3. statusとサービスのTLS応答を確認してから、タイマーを再開します。

バックアップの復元は、先にテスト用の配置先で試してください。秘密鍵には元と同等のアクセス制限を適用します。

注意:

- 配置先の `.pull-guard` はプロセス間の排他に使うため、削除しないでください。
- 自動復旧に対応したバージョンへ切り替える前に、旧バージョンのPullとタイマーを停止してください。

## 定期実行

手動のPullとTLS接続を確認したら、[service例](../configs/systemd/certpouch-pull.service)と[timer例](../configs/systemd/certpouch-pull.timer)を `/etc/systemd/system/` に置きます。例は、バイナリを `/usr/local/bin/certpouch` に置き、システムサービスの既定ユーザーで実行する想定です。

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now certpouch-pull.timer
systemctl list-timers certpouch-pull.timer
```

- 実行周期は1時間で、ホスト間の同時実行を避けるため最大5分ずらします。
- 失敗時は `journalctl -u certpouch-pull.service` と `certpouch status` で確認します。
- 変更を止めるにはtimerを停止します。実行中のserviceがあれば、終了を待ってから配置状態を確認します。
