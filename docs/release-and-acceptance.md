# リリースと運用受け入れ

リリース候補の作成と、既存環境へ導入する前に確認する項目をまとめます。

## リリース候補

### 準備

- Go 1.26.11以降と[Task v3.54.0](https://github.com/go-task/task/releases/tag/v3.54.0)を用意します。
- リリース対象のコミットを、きれいなチェックアウトで開きます。

| タスク | 内容 |
| --- | --- |
| `task build` | 現在の環境向けのバイナリを `dist/` に出力します。出力先は `BUILD_OUTPUT` で指定できます |
| `task test` | Goテストを実行します |
| `task verify` | `go vet` とraceテストを実行します |

### 梱包と検証

LinuxまたはGit Bashで次を実行すると、`dist/` にLinux amd64・arm64のtar.gzと `SHA256SUMS` を生成して検証します。アーカイブには、同じコミットのREADME、設定例、systemdユニット例、運用文書、ブランド資産が入ります。

```sh
task release:package RELEASE_VERSION=v0.2.1
task release:verify RELEASE_VERSION=v0.2.1
```

| 変数 | 内容 |
| --- | --- |
| `RELEASE_VERSION` | `--version` に埋め込む安定版のタグ。省略時は `dev` |
| `OUTPUT_DIR` | 出力先。変える場合は両方のタスクに同じ値を渡します |
| `RELEASE_COMMIT` | `release:verify` の比較対象。既定は現在の `HEAD` |

`release:verify` は、SHA-256と、内部のバイナリのOS・アーキテクチャ・コミットID・未コミット変更の有無を検証します。Linux amd64では、展開したバイナリの起動と、インストール・アンインストールも確認します。

### GitHub Actions

| ワークフロー | 動作 |
| --- | --- |
| `Verify` | mainへのpushとPull Requestで、`task verify` と `task test:installer` を実行します |
| `Release` | Taskfileがあるタグでは梱包・検証タスクを使います。それより前のタグを再梱包するときは、そのタグのスクリプトを使います |

リリースの流れ:

1. mainへのpushを受けて、Release PleaseがConventional CommitsからバージョンとCHANGELOGを更新するリリースPRを作成します。初回の候補は `v0.1.0` です。
2. リリースPRをマージすると、Release PleaseがタグとGitHub Releaseの下書きを作成します。同じワークフローがタグのコミットを検証・梱包し、2種類のアーカイブと `SHA256SUMS` を下書きに添付します。
3. 検証と添付が成功し、配布物3点がそろった時点で、ワークフローが安定版を公開します。公開前に処理が失敗した場合は、下書きが残ります。

リリースノートと受け入れ結果は、リリースPRをマージする前に確認してください。

**手動実行**

- `release_tag` を空にすると、ワークフローの成果物だけを生成します。
- 既存の下書きのタグ（例 `v0.1.0`）を入力すると、そのコミットから作り直した成果物を下書きに添付できます。手動で再梱包した下書きは、成果物を確認してから公開します。

**リポジトリの設定**

- リリースPRを作成するには、Settings → Actions → General で **Allow GitHub Actions to create and approve pull requests** を有効にします。
- 組み込みの `GITHUB_TOKEN` を使うため、リリースPRで必須チェックが待機する場合は、GitHub上で実行を承認するか、リポジトリのCI運用に合わせて起動方法を設定します。[Release Pleaseの権限とCI](https://github.com/googleapis/release-please-action#github-credentials)

## DNSとSheets

1. Sheetsのテスト用台帳と読み取り専用サービスアカウントで、隔離した場所のキャッシュへ取得します。期待した範囲だけが取得され、前回の成功記録と内容が一致することを確認します。
2. 同じ台帳を旧Python版とCertPouchに与え、正引きと逆引きのレコードセットを比較します。SOA、NS、TTL、固定レコード、IDNと空欄の扱いを確認し、差異は移行前に説明できるよう記録します。
3. 現行のNSD設定とゾーンを別途退避し、隔離したNSDで候補を検証して適用します。実運用での削除・置換の上限を決め、配信中のSOA serialと、代表的なA・PTR・固定レコードを確認します。

### 実NSDでの通し試験

隔離したLinuxで `nsd`、`nsd-control`、`nsd-checkconf`、`nsd-checkzone` をPATHに置き、次を実行します。NSDは一時設定とループバックで起動します。

```sh
CERTPOUCH_TEST_NATIVE_PIPELINE=1 go test -count=1 ./cmd/certpouch \
  -run '^(TestPipelineSheetsToCertificatePublication|TestNativeNSDActivation)$'
```

- 模擬Sheets応答による初回のゾーン登録、更新、別名の削除と証明書発行を確認できます。
- 実NSDで、逆引きゾーンの追加・削除と、切り戻し前後のA・PTR応答も確認します。

## KeycloakとCA、バックアップ

1. 運用と同じサービスユーザー・グループ・ユニットを隔離環境で使い、鍵の読み取り権限と書き込み拒否、再起動と旧世代への切り戻しを確認します。
2. 実運用と同じCA鍵の保護方式をテストCAで再現し、更新タイマーから公開・配布・Pullまでを通します。発行に失敗しても現行の公開世代が維持され、CA秘密鍵が配布サービスの実行ユーザーから読めないことを確認します。
3. 配置先・同期状態・設定・信頼CAのバックアップを隔離環境へ復元します。秘密鍵の所有者と権限、`status`、サービスのTLS応答を確認します。

## セキュリティ確認

リリース前に次を確認します。

- 接続元IPとクライアント認証
- 証明書ごとの認可
- 失効時の拒否
- 秘密情報を含まないエラーとログ
- 世代の切り替えに失敗したときの復旧
- 依存関係と対象環境のセキュリティ更新状況

運用の鍵と資格情報は、リポジトリ、テスト、CIの成果物に含めないでください。

Goコードの既知の脆弱性は、次のコマンドで調べます。依存モジュール内の報告と、アプリケーションの呼び出し経路にある報告の両方を確認します。[公式のGo脆弱性管理資料](https://go.dev/doc/security/vuln/)

```sh
go run golang.org/x/vuln/cmd/govulncheck@v1.10.0 ./...
```
