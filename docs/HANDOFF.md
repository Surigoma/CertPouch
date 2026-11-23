# Phase 1 実装引き継ぎ

文書のコミットは `0bd019c`。Goモジュールと `internal/pki` の初回実装は作業ツリーにある。着手前に `git status --short` で現在の状態を確認する。

## 実装済み

- Go 1.25のモジュールを追加。配布APIのルーティングと共通処理にはGin v1.12.0を使用する。
- `pki.Validate` は証明書・チェーン・秘密鍵を受け取り、呼び出し元が設定した信頼CA、発行者、期待SAN、検証時刻に基づいて検証する。正常時はリーフとDERのSHA-256フィンガープリントを返す。
- テストCAを使い、正常な中間CAチェーンと、鍵不一致、SAN不一致、誤った発行者、未知CA、中間CA欠落、EKU欠落、余分なPEMデータ、期限切れの拒否を確認済み。
- `internal/distribution` に検証済み世代のメモリ内カタログと配布APIを追加した。HTTPS、トークンハッシュ、接続元IP、証明書割り当てを検証する。
- `internal/server` にTLS 1.2以上での起動とcontext終了時の停止処理を追加した。
- Ginのプロキシ信頼と自動リダイレクトを無効化し、IP許可判定は接続元アドレスを使う。
- WindowsのGo 1.25.3で `go test ./...` と `go vet ./...` が通過した。

## 次の作業

1. [設定仕様](SPEC/02-configuration.md)と[API仕様](SPEC/03-distribution-api.md)の提案を確認し、設定スキーマとYAML依存を決める。クライアントはトークンを保持し、サーバーはハッシュだけを保持する。
2. 永続ストアから不変世代を読み出し、既存の配布APIに渡す。設定とストアをCLIから接続し、Linuxで起動を確認する。
3. Pullと配置を実装する。整合性とローカルの検証ポリシーを確認してから切り替え、サービス操作が失敗したら旧世代へ復旧する。[配置仕様](SPEC/04-certificate-deployment.md)
4. 設定例、systemd timer例、Linux 1ホストへの導入手順を用意する。Keycloakの証明書形式とreload/restartの動作は隔離環境で確認する。[受け入れ条件](SPEC/06-verification-operations.md)

設定読み込み、永続ストア、CLI、Pullと配置は未実装。Goの検証ではLinuxのファイル権限、systemd、Keycloak連携は確認していない。初回実装の範囲は証明書配布とし、DNS同期とCAによる発行は後続フェーズで扱う。
