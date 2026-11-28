# CertPouch 実装引き継ぎ

着手前に `git status --short` で現在の状態を確認する。作業ツリーにある既存の `.gitignore` 変更と、`.orig/` の移行資料は作業者の管理下にある。移行資料に本番情報が含まれる可能性があるため、コミットやログ出力の対象にしない。

証明書の登録・発行、HTTPS配布、LinuxクライアントでのPull・配置・切り戻し、SheetsまたはCSVからのNSD候補生成と適用に対応する。操作を始めるときは[初回導入](quickstart.md)、動作を確認するときは[詳細仕様](SPEC/README.md)を参照する。

## 実装済み

- Linuxリリースに、バイナリだけを安全に配置・削除する `install.sh` と `uninstall.sh` を追加した。設定、資格情報、証明書、状態、systemd unitは保持し、管理外ファイルの上書きと変更済みバイナリの削除を既定で拒否する。[Linuxへのインストールと削除](linux-installation.md)
- サーバー側の配布、Sheets取得、DNS生成、証明書更新は `server.yaml`、取得・配置は `client.yaml` に集約する。`server.yaml` は `inventory`・`dns`・`certificates`・`distribution` の4節からなるv2形式だけを読み、v1のトップレベルキーは移行先を示すエラーで拒否する。YAMLエラーは値を表示せず行・列と未知・重複キー名を示す。`configure dns` と `configure server` もv2形式を出力する。[設定コマンド](configure.md)・[設定仕様](SPEC/02-configuration.md)
- Go 1.26.11以降を使用する。配布APIのルーティングと共通処理にはGin v1.12.0を使用する。
- `pki.Validate` は証明書・チェーン・秘密鍵を受け取り、呼び出し元が設定した信頼CA、発行者、期待SAN、検証時刻に基づいて検証する。正常時はリーフとDERのSHA-256フィンガープリントを返す。
- テストCAを使い、正常な中間CAチェーンと、鍵不一致、SAN不一致、誤った発行者、未知CA、中間CA欠落、EKU欠落、余分なPEMデータ、期限切れの拒否を確認済み。
- `internal/distribution` に検証済み世代のメモリ内カタログと配布APIを追加した。HTTPS、トークンハッシュ、接続元IP、証明書割り当てを検証する。
- `internal/server` にTLS 1.2以上での起動とcontext終了時の停止処理を追加した。
- Ginのプロキシ信頼と自動リダイレクトを無効化し、IP許可判定は接続元アドレスを使う。
- `internal/storage` は管理対象の `current` リンクを解決して、配布APIが検証する証明書世代を読み出す。
- `configure client` と `configure server` は新規ディレクトリに検証済みの初期設定と配置先・公開ストアを作成する。配布サーバーへ渡す資格情報はクライアントトークンのハッシュ。[初期化手順](quickstart.md)
- `certpouch server --config PATH` はYAMLと既存の公開ストアを検証してHTTPS配布サーバーを起動する。構成例は [server.example.yaml](../configs/server.example.yaml) を参照。
- DNS台帳モードでは、Google Sheetsの既存キャッシュにある証明書名と接続元IPの一致で取得を認可する。別ホストへの許可はクライアント・証明書単位の例外設定で追加する。[DNS台帳の設定手順](dns-inventory.md)
- `internal/client` はHTTPSで在庫を取得し、選択世代のダウンロードを固定したうえでチェックサム・フィンガープリント・ローカル証明書ポリシーを検証する。
- `certpouch pull --dry-run --config PATH` は、許可された在庫と設定済み配置先を比較し、`install`、`update`、`unchanged` を表示する。[クライアント設定例](../configs/client.example.yaml)
- Linuxの通常の `pull` は検証済み世代へ切り替え、systemdの反映に失敗すれば旧世代へ戻す。[配置と手動復旧の手順](client-deployment.md)
- `status --config PATH` は公開証明書の情報、配置状態、正常なPullで記録した最終同期時刻をローカルから表示する。
- 異常終了後の次回Pullは、旧世代と復旧記録を確認できれば、サーバーへの接続前に旧世代を再反映する。旧世代がない場合などは手動復旧が必要。
- `cert publish` は既存CAが発行した証明書を検証し、公開ストアへ新しい世代として登録する。[登録手順](server-publication.md)
- Linuxの通しテストで、テストCAによる登録、HTTPS配布、初回Pull、サーバー再起動なしの世代更新、同一世代の再Pull、オフラインの `status` を確認する。既定ではサービス操作をテスト用の代替コマンドで検証する。
- systemdの定期実行例と[導入手順](client-deployment.md)を追加した。
- WSL上の一時的なsystemdサービスとタイマーで、初回配置とタイマーによる世代更新の実リロード、同一世代の再Pullでリロードしないことを確認した。Linuxで再現するには管理権限で `CERTPOUCH_TEST_SYSTEMD=1 go test -run TestLinuxPublicationAndPullWorkflow ./cmd/certpouch` を実行する。テスト専用の一時unitは終了時に停止する。
- 専用ユーザーで起動する配布サーバー用unit例を追加した。[サーバーの設定手順](server-publication.md)
- `server --check-config` は待ち受けを開始せずに設定、TLS鍵ペア、公開世代を検証する。WSLでは同梱unitの実行ユーザーとパスをテスト用に差し替え、非特権ユーザーで起動しHTTPS healthが応答することを確認した。
- クライアントの `service.verify_tls` を指定すると、サービスがアクティブ世代の証明書を返すことを配置後と復旧時に確認できる。[Keycloak向け設定例](client-deployment.md)
- `dns sync` は既存キャッシュまたはCSVからNSD向けゾーン候補を生成する。`dns apply` は専用管理ディレクトリで候補と切り戻し用の世代を検証し、NSDへの反映をSOA serialで確認する。[DNSゾーン生成と適用](dns-sync.md)
- DNS設定で固定レコードを追加できる。`dns sync --serial-from` で管理中の `current` から次のserialを計算し、`dns diff` で適用前の差分を確認できる。
- `dns fetch` はGoのOAuth2ライブラリでサービスアカウントを認証し、公式Sheetsクライアントで台帳を取得する。全範囲と内容の検証に通った場合だけキャッシュを置き換える。[取得設定と定期実行](sheets-fetch.md)
- 配布サーバーは `distribution.refresh_interval` ごとに、キャッシュ・CSVのIP認可をサーバー再起動なしで反映する。不正な台帳を読み込んだ場合は回復まで台帳モードの認証を停止する。
- `dns fetch` はキャッシュと照合できる最終成功記録を保存する。`dns fetch --status` はその状態をローカルで表示し、`inventory.max_age` は期限切れの台帳による新規配布要求を拒否する。[キャッシュの期限管理](dns-inventory.md)
- 配布APIはオプションでmTLSを使える。クライアント証明書のCA検証とクライアントIDへのフィンガープリント照合を追加し、従来のトークン・IP認可も維持する。[mTLSの設定](mtls.md)
- オプションの失効リストで、クライアントIDまたはmTLSリーフを配布サーバーの再起動なしで拒否できる。[失効の設定](client-revocation.md)
- `cert renew` は既存のRSA CAを使い、指定した証明書の初回発行・期限更新・公開を行う。dry-runではCA秘密鍵を読まない。暗号化PEMはOpenSSLでメモリ上に復号する。[証明書更新の設定と運用](certificate-renewal.md)
- 配布サーバーは同じ間隔で公開ストアを定期的に読み直す。署名を管理者として実行した場合も、新しい世代はストアの所有者に合わせて保存する。証明書更新用systemd timerの例を追加した。
- `cert status` は公開ストア上の証明書の期限をローカルで監視する。配布サーバーに接続せず、公開リーフだけを読む。[監視コマンド](certificate-renewal.md#公開証明書の期限監視)
- 配布APIは一覧・メタデータ・ダウンロードの結果を構造化ログに記録する。定期的なストア検証に失敗したときはhealthが503を返す。[配布API仕様](SPEC/03-distribution-api.md)
- Release PleaseがリリースPRを管理する。マージ後にタグと下書きを作成し、検証済みLinux amd64・arm64パッケージを添付する。[リリース手順](release-and-acceptance.md)
- WSLの一時的なsystemdタイマーで、テスト用中間CAによる期限切れ証明書の更新を確認した。`CERTPOUCH_TEST_SYSTEMD=1 go test -run TestRenewWithPrivateCA ./cmd/certpouch` で再現できる。

## 検証済みの範囲

| 対象 | 確認内容 |
| --- | --- |
| HTTPS配布・Pull | WSLのループバックと実systemdで、初回配置、タイマー更新、同一世代の再Pull、ローカルstatusを確認した。非特権ユーザーのreload処理が0640の秘密鍵と証明書を読み込めること、鍵と世代ディレクトリに書き込めないことを確認した。旧証明書が返り続けた場合は、不一致の報告、旧世代の再反映、更新の再試行も確認した。 |
| Keycloak | WSL上のKeycloak 26.8.2と一時systemdユニットで、HTTPS配布APIからの初回Pull、再起動失敗後の旧証明書の再配信、更新の再試行を確認した。Keycloakを使う検証は環境変数で明示的に有効化する。 |
| 認可と失効 | DNS台帳のIP変更と期限切れ、mTLS証明書とトークンの照合、ID・証明書の失効、不正な失効リストからの復帰をWSLの通しテストで確認した。 |
| CA更新 | 生成した中間CAを使い、新しい鍵の発行、暗号化PEM、世代競合の拒否、更新タイマーを確認した。 |
| DNS | WSLの一時NSD 4.8で、`nsd-checkzone`、正引き・逆引き候補の適用、SOA serialの引き継ぎ、切り戻しを確認した。提供されたCSVは候補生成まで確認した。 |
| DNS移行の事前確認 | `dns diff --baseline` で登録前の候補同士を比較できる。基準候補のSOA serialは `dns sync --serial-from-baseline` で引き継げる。登録後は `dns apply --max-removed-records` でレコードの削除・置換件数に上限を設けられる。 |
| 旧PythonとのDNS比較 | 提供CSVの有効22行から正引き1・逆引き2ゾーンを生成し、`nsd-checkzone` 4.8.0を通過した。旧Pythonの空ラベル1件を比較対象から除くと、A 40件・PTR 40件がすべて一致した。旧テンプレート、固定レコード、配信中のゾーンは移行時に確認する。 |
| Google Sheets | 模擬HTTPSでOAuthと範囲取得、不完全な応答の拒否を確認した。実際のアカウントに対する通信は未検証。 |

通常の確認は `go test ./...` と `go vet ./...`。HTTPSテストにはループバック接続が必要。Linux固有のテストはWindowsの通常実行には含まれない。実systemd経由の検証には隔離したLinux環境で `CERTPOUCH_TEST_SYSTEMD=1` を指定する。NSDの通し検証には `nsd`、`nsd-control`、`nsd-checkzone` が必要。

## 次の作業

1. 運用と同じKeycloak用systemdユニットとサービスユーザーを使い、隔離したホストで通し試験を再実行し、鍵へのアクセス権限と再起動を確認する。[配置手順](client-deployment.md)・[受け入れ条件](SPEC/06-verification-operations.md)
2. 既存NSDゾーンと出力を比較し、必要な固定レコードを移行する。管理ゾーンの追加・削除の手順を検討する。[DNS仕様](SPEC/05-dns-pki.md)
3. 本番のCA保護方式と専用ユーザーを使い、隔離環境で更新timerから配布まで検証する。
4. 実際のサービスアカウントで台帳取得を検証し、既存キャッシュとの比較と切り替えを行う。

定期読み込みを有効にすると、公開世代とDNS台帳の変更は再起動なしで反映できる。設定ファイル、トークン、TLS鍵などの変更は再起動で反映する。本番DNS、既存CAとの連携は別途確認する。

リリース候補の作成、バックアップ復元、運用環境での確認項目は[リリースと運用受け入れ](release-and-acceptance.md)を参照する。
