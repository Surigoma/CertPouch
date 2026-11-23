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
- WindowsのGo 1.25.3で `go test ./...` と `go vet ./...` が通過した。

## 次の作業

1. Linuxでサーバー起動とファイル権限を確認する。現在のストア読み込みは起動時の公開世代を対象とする。
2. サーバー側の証明書世代を安全に登録・更新する手順を実装する。[配置仕様](SPEC/04-certificate-deployment.md)
3. クライアント設定例、systemd timer例、Linux 1ホストへの導入手順を用意する。Keycloakの証明書形式とreload/restartの動作は隔離環境で確認する。[受け入れ条件](SPEC/06-verification-operations.md)

世代の登録・更新は未実装。実際のsystemdとKeycloakの連携は隔離環境で確認する。初回実装の範囲は証明書配布とし、DNS同期とCAによる発行は後続フェーズで扱う。
