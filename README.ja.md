# CertPouch

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
  <img src="assets/logo-light.svg" width="300" alt="CertPouch">
</picture>

[English](README.md)

**Private certificates, delivered simply.**

CertPouchは、プライベートDNS、プライベートPKI、Pull型の証明書配布を扱うセルフホスト型のプロジェクトです。既存のプライベートCAを利用し、単一のGoバイナリで動作する設計です。

**開発状況** — 証明書配布、NSDゾーン管理、既存プライベートCAでの更新を実装済みです。サービス配置はLinuxとsystemdを対象とします。既存環境を切り替える前に、環境に合わせた受け入れ確認を行ってください。[リリースと運用受け入れ](docs/release-and-acceptance.md)

## 仕組み

中央サーバーで証明書と秘密鍵を保管します。DNS台帳モードでは、認証済みホストの接続元IPがGoogle Sheetsのスナップショット上で証明書名に対応するIPと一致すると取得できます。別ホストからの取得も例外設定で許可できます。Pullクライアントは、証明書を検証してから新しい世代に切り替え、サービス操作に失敗した場合は切り戻します。[DNS台帳の設定](docs/dns-inventory.md)

## 最初に用意する設定

Linuxリリースには、運用データを保持したままバイナリを配置・削除するスクリプトが含まれます。[Linuxへのインストールと削除](docs/linux-installation.md)

設定値の入手先、トークンの作成、初回Pullまでを順に確認できます。[証明書を1台に配布する手順](docs/quickstart.md)

既存のDNS台帳キャッシュまたはCSVで証明書配布を始める場合、YAMLはサーバーに1枚、取得するホストごとに1枚です。TLS証明書、CA、トークンなどのファイルは各YAMLから参照します。

- サーバーでは `server.yaml` を `server` と `cert publish` で共用します。[サーバー設定例](configs/server.example.yaml)
- 取得ホストでは `client.yaml` を `pull` と `status` で共用します。[クライアント設定例](configs/client.example.yaml)

管理する設定YAMLは `server.yaml` と `client.yaml` の2枚です。サーバー側の配布、Sheets取得、DNS生成、CA更新は `server.yaml` にまとめます。資格情報、証明書、任意の実行時失効リストは保護対象データとして別ファイルに保ちます。[Sheets取得](docs/sheets-fetch.md)・[DNS生成](docs/dns-sync.md)・[CA更新](docs/certificate-renewal.md)・[クライアント失効](docs/client-revocation.md)

## 利用できる機能

- 発行済み証明書を公開し、クライアント認証と証明書単位の接続元IP制御を使ってHTTPSで配布します。配置に失敗すると旧世代へ切り戻します。[サーバー設定](docs/server-publication.md)・[クライアント配置](docs/client-deployment.md)
- DNS台帳からNSD向けの正引き・IPv4逆引き候補を生成し、差分を確認して検証済み候補を適用します。[DNSの生成と適用](docs/dns-sync.md)
- 既存のプライベートCAで証明書を発行・更新します。systemdタイマーで定期更新できます。[証明書の更新](docs/certificate-renewal.md)
- 相互TLSを有効化し、サーバー稼働中にクライアント失効を反映できます。[相互TLS](docs/mtls.md)・[クライアント失効](docs/client-revocation.md)

## CLI

| コマンド | 用途 |
| --- | --- |
| `certpouch configure dns` | CUIまたはフラグでDNS・Sheets取得設定を生成 |
| `certpouch configure server` / `certpouch configure client` | サーバー・取得ホストの初期設定を作成 |
| `certpouch pipeline --watch` | Sheets・NSD・証明書を定期同期 |
| `certpouch server` | HTTPS配布サーバーを起動 |
| `certpouch pull` | 証明書の更新を取得して配置 |
| `certpouch pull --dry-run` | 更新予定を確認 |
| `certpouch status` | ローカルの証明書と配置状況を確認 |

個別に実行する場合の操作と診断コマンドです。

| コマンド | 用途 |
| --- | --- |
| `certpouch cert publish` | 発行済みの証明書を新しい配布世代として登録 |
| `certpouch dns sync --dry-run` | DNS台帳から生成するNSD向けゾーンを確認 |
| `certpouch dns fetch` | Google SheetsからDNS台帳を取得してキャッシュを更新 |
| `certpouch dns apply` | 検証した候補を管理下のNSDへ適用 |
| `certpouch dns diff` | DNS候補と現行または基準のスナップショットを比較 |
| `certpouch cert renew` | 既存のプライベートCAで対象証明書を発行・更新 |
| `certpouch cert status` | 公開ストア上の証明書の期限と状態を確認 |

自動運用は `server.yaml` 一枚で設定します。[DNSゾーン生成と適用](docs/dns-sync.md)・[証明書の登録手順](docs/server-publication.md)・[証明書の更新手順](docs/certificate-renewal.md)

## 仕様とライセンス

- [現行実装の全体仕様（英語）](SPEC.md)
- [詳細仕様書](docs/SPEC/README.md)
- [Taskでのビルド・テスト・リリース梱包](docs/release-and-acceptance.md#リリース候補)
- [DNS台帳の記入例](docs/dns-inventory.md#台帳の記入例)
- [相互TLSの設定](docs/mtls.md)
- [MITライセンス](LICENSE)
