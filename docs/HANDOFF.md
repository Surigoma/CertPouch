# Phase 1 実装引き継ぎ

文書のコミットは `0bd019c`、配布APIの初回実装は `d5cc743`。着手前に `git status --short` で現在の状態を確認する。

## 実装済み

- Go 1.25のモジュールを追加。配布APIのルーティングと共通処理にはGin v1.12.0を使用する。
- `pki.Validate` は証明書・チェーン・秘密鍵を受け取り、呼び出し元が設定した信頼CA、発行者、期待SAN、検証時刻に基づいて検証する。正常時はリーフとDERのSHA-256フィンガープリントを返す。
- テストCAを使い、正常な中間CAチェーンと、鍵不一致、SAN不一致、誤った発行者、未知CA、中間CA欠落、EKU欠落、余分なPEMデータ、期限切れの拒否を確認済み。
- `internal/distribution` に検証済み世代のメモリ内カタログと配布APIを追加した。HTTPS、トークンハッシュ、接続元IP、証明書割り当てを検証する。
- `internal/server` にTLS 1.2以上での起動とcontext終了時の停止処理を追加した。
- Ginのプロキシ信頼と自動リダイレクトを無効化し、IP許可判定は接続元アドレスを使う。
- `internal/storage` は管理対象の `current` リンクを解決して、配布APIが検証する証明書世代を読み出す。
- `certpouch server --config PATH` はYAML設定を読み込み、ストアを初期化してHTTPS配布サーバーを起動する。構成例は [server.example.yaml](../configs/server.example.yaml) を参照。
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
- `cert renew` は既存のRSA CAを使い、指定した証明書の初回発行・期限更新・公開を行う。dry-runではCA秘密鍵を読まない。暗号化PEMはOpenSSLでメモリ上に復号する。[証明書更新の設定と運用](certificate-renewal.md)
- `server.publication_refresh` は公開ストアを定期的に読み直す。署名を管理者として実行した場合も、新しい世代はストアの所有者に合わせて保存する。証明書更新用systemd timerの例を追加した。
- `cert status` は公開ストア上の証明書の期限をローカルで監視する。配布サーバーに接続せず、公開リーフだけを読む。[監視コマンド](certificate-renewal.md#公開証明書の期限監視)
- WSLの一時的なsystemdタイマーで、テスト用中間CAによる期限切れ証明書の更新を確認した。`CERTPOUCH_TEST_SYSTEMD=1 go test -run TestRenewWithPrivateCA ./cmd/certpouch` で再現できる。
- WindowsのGo 1.25.3で `go test ./...` と `go vet ./...` が通過した。

## 次の作業

1. Keycloak 1ホストの証明書参照形式、更新後のTLS応答、切り戻しを隔離環境で確認する。[受け入れ条件](SPEC/06-verification-operations.md)
2. 既存NSDゾーンと出力を比較し、必要な固定レコードを移行する。管理ゾーンの追加・削除の手順を検討する。[DNS仕様](SPEC/05-dns-pki.md)
3. 本番のCA保護方式と専用ユーザーを使い、隔離環境で更新timerから配布まで検証する。

公開ストアの定期読み込みを設定した場合、登録・更新後に配布サーバーを再起動する必要はない。認可設定やTLS鍵などの変更は再起動で反映する。Keycloak、本番DNS、既存CAとの連携は別途確認する。
