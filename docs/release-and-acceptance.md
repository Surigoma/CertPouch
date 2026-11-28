# リリースと運用受け入れ

## リリース候補

Go 1.26.11以降と[Task v3.54.0](https://github.com/go-task/task/releases/tag/v3.54.0)を用意し、リリース対象コミットをきれいなチェックアウトで開く。`task build` は現在の環境向けバイナリを `dist/` に出力する。`BUILD_OUTPUT` で出力先を指定できる。`task test` はGoテスト、`task verify` は `go vet` とraceテストを実行する。

LinuxまたはGit Bashで次を実行すると、`dist/` にLinux amd64・arm64のtar.gzと `SHA256SUMS` を生成して検証する。アーカイブには同じコミットのREADME、設定例、systemdユニット例、運用文書、ブランド資産を収録する。

```sh
task release:package RELEASE_VERSION=v0.2.1
task release:verify RELEASE_VERSION=v0.2.1
```

`RELEASE_VERSION` は `--version` に埋め込む安定版タグで、省略時は `dev`。出力先を変える場合は両タスクに同じ `OUTPUT_DIR` を渡す。`release:verify` はSHA-256と、内部バイナリのOS・アーキテクチャ・コミットID・未コミット変更の有無を検証する。比較対象は既定で現在の `HEAD` で、`RELEASE_COMMIT` で指定できる。Linux amd64では、展開したバイナリの起動とインストール・アンインストールも確認する。

GitHub Actionsの `Verify` はmainへのpushとPull Requestで `task verify` と `task test:installer` を実行する。`Release` はTaskfileがあるタグで梱包・検証タスクを使い、それ以前のタグを再梱包するときは、そのタグのスクリプトを使う。

1. mainへのpushを受け、Release PleaseがConventional CommitsからバージョンとCHANGELOGを更新するリリースPRを作成する。初回の候補は `v0.1.0`。
2. リリースPRをマージすると、Release PleaseがタグとGitHub Releaseの下書きを作成する。同じワークフローがタグのコミットを検証・梱包し、2種類のアーカイブと `SHA256SUMS` を下書きに添付する。
3. 検証と添付が成功し、配布物3点がそろった時点でワークフローが安定版を公開する。リリースノートと受け入れ結果は、リリースPRをマージする前に確認する。公開前に処理が失敗した場合は下書きが残る。

`Release` の手動実行では、`release_tag` を空にするとワークフローの成果物だけを生成する。既存の下書きのタグを `v0.1.0` のように入力すると、そのコミットから作り直した成果物を下書きに添付できる。手動で再梱包した下書きは、成果物を確認してから公開する。

リリースPRを作成するには、リポジトリの Settings → Actions → General で **Allow GitHub Actions to create and approve pull requests** を有効にする。組み込みの `GITHUB_TOKEN` を使うため、リリースPRで必須チェックが待機する場合はGitHub上で実行を承認するか、リポジトリのCI運用に合わせて起動方法を設定する。[Release Pleaseの権限とCI](https://github.com/googleapis/release-please-action#github-credentials)

## DNSとSheets

1. Sheetsのテスト用台帳と読み取り専用サービスアカウントを使い、隔離先のキャッシュへ取得する。期待した範囲だけが取得され、前回成功記録と内容が一致することを確認する。
2. 同じ台帳を旧Python版とCertPouchに与える。正引きと逆引きのレコードセットを比較し、SOA、NS、TTL、固定レコード、IDNと空欄の扱いを確認する。差異は移行前に説明できるよう記録する。
3. 現行NSD設定とゾーンは別途退避し、隔離したNSDで候補を検証して適用する。実運用用の削除・置換上限を決め、配信中のSOA serialおよび代表的なA・PTR・固定レコードを確認する。

隔離Linuxで `nsd`、`nsd-control`、`nsd-checkconf`、`nsd-checkzone` をPATHに置き、`CERTPOUCH_TEST_NATIVE_PIPELINE=1 go test -count=1 ./cmd/certpouch -run '^(TestPipelineSheetsToCertificatePublication|TestNativeNSDActivation)$'` を実行する。模擬Sheets応答による初回ゾーン登録、更新、別名の削除と証明書発行を確認できる。実NSDで逆引きゾーンの追加・削除と、切り戻し前後のA・PTR応答も確認する。NSDは一時設定とループバックで起動する。

## KeycloakとCA、バックアップ

1. 運用と同じサービスユーザー・グループ・ユニットを隔離環境で使い、鍵の読取権限と書込拒否、再起動と旧世代への切り戻しを確認する。
2. 実運用と同じCA鍵保護方式をテストCAで再現し、更新タイマーから公開・配布・Pullまで通す。発行失敗時は現行の公開世代が維持され、CA秘密鍵が配布サービスの実行ユーザーから読めないことを確認する。
3. 配置先・同期状態・設定・信頼CAのバックアップを隔離環境へ復元する。秘密鍵の所有者と権限、`status`、サービスのTLS応答を確認する。

## セキュリティ確認

リリース前に、接続元IPとクライアント認証、証明書ごとの認可、失効時の拒否、秘密情報を含まないエラー・ログ、世代切り替え失敗時の復旧を確認する。依存関係と対象環境のセキュリティ更新状況も点検対象とする。運用鍵と資格情報はリポジトリ、テスト、CI成果物に含めない。

Goコードの既知脆弱性は `go run golang.org/x/vuln/cmd/govulncheck@v1.10.0 ./...` で調べる。依存モジュール内の報告と、アプリケーションの呼び出し経路にある報告を確認する。[公式のGo脆弱性管理資料](https://go.dev/doc/security/vuln/)
