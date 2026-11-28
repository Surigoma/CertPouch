# CertPouch

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
  <img src="assets/logo-light.svg" width="300" alt="CertPouch">
</picture>

[English](README.md)

**Private certificates, delivered simply.**

CertPouchは、プライベートDNS、プライベートPKI、Pull型の証明書配布を扱うセルフホスト型のツールです。既存のプライベートCAを利用し、単一のGoバイナリで動作します。

> **開発状況:** 証明書配布、NSDゾーン管理、既存プライベートCAでの更新を実装済みです。サービスの配置はLinuxとsystemdを対象とします。既存環境を切り替える前に、[リリースと運用受け入れ](docs/release-and-acceptance.md)の確認を行ってください。

## 仕組み

1. 中央サーバーが証明書と秘密鍵を保管します。
2. 取得ホストは認証したうえで、必要な証明書を要求します。DNS台帳モードでは、接続元IPがGoogle Sheetsのスナップショット上で証明書名に対応するIPと一致すると取得できます。別ホストからの取得も例外設定で許可できます。
3. Pullクライアントは証明書を検証してから新しい世代に切り替え、サービス操作に失敗した場合は切り戻します。
4. サーバーは再起動なしで新しい公開世代を読み込みます。

詳しくは[DNS台帳の設定](docs/dns-inventory.md)を参照してください。

## はじめに

1. バイナリを配置します。Linuxリリースには、運用データを保持したまま配置・削除するスクリプトが含まれます。[Linuxへのインストールと削除](docs/linux-installation.md)
2. [QuickStart](docs/quickstart.md)で、Sheetsからの自動同期か、発行済み証明書の手動配布かを選び、選んだ手順で初期設定を作ります。

### 設定ファイル

管理するYAMLは次の2つです。TLS証明書、CA、トークンなどのファイルは各YAMLから参照します。

| ファイル | 置く場所 | 使うコマンド | 設定例 |
| --- | --- | --- | --- |
| `server.yaml` | サーバー | `server`、`pipeline`、`dns ...`、`cert ...` | [server.example.yaml](configs/server.example.yaml) |
| `client.yaml` | 取得ホストごと | `pull`、`status` | [client.example.yaml](configs/client.example.yaml) |

配布、Sheets取得、DNS生成、CA更新の設定は `server.yaml` にまとめます。資格情報、証明書、任意の実行時失効リストは保護対象データとして別ファイルに置きます。[Sheets取得](docs/sheets-fetch.md)・[DNS生成](docs/dns-sync.md)・[CA更新](docs/certificate-renewal.md)・[クライアント失効](docs/client-revocation.md)

## 利用できる機能

| 機能 | 手順 |
| --- | --- |
| 発行済み証明書を公開し、クライアント認証と証明書単位の接続元IP制御でHTTPS配布する。配置に失敗すると旧世代へ切り戻す | [サーバー設定](docs/server-publication.md)・[クライアント配置](docs/client-deployment.md) |
| DNS台帳からNSD向けの正引き・IPv4逆引き候補を生成し、差分を確認して検証済み候補を適用する | [DNSの生成と適用](docs/dns-sync.md) |
| 既存のプライベートCAで証明書を発行・更新する。systemdタイマーで定期実行できる | [証明書の更新](docs/certificate-renewal.md) |
| 相互TLSを有効化し、サーバー稼働中にクライアント失効を反映する | [相互TLS](docs/mtls.md)・[クライアント失効](docs/client-revocation.md) |

## CLI

### 普段使うコマンド

| コマンド | 用途 |
| --- | --- |
| `certpouch configure dns` | 対話入力またはフラグでDNS・Sheets取得設定を生成 |
| `certpouch configure server` / `certpouch configure client` | サーバー・取得ホストの初期設定を作成 |
| `certpouch pipeline --watch` | Sheets・NSD・証明書を定期同期 |
| `certpouch server` | HTTPS配布サーバーを起動 |
| `certpouch pull` | 証明書の更新を取得して配置 |
| `certpouch pull --dry-run` | 更新予定を確認 |
| `certpouch status` | ローカルの証明書と配置状況を確認 |

### 個別操作と診断

| コマンド | 用途 |
| --- | --- |
| `certpouch cert publish` | 発行済みの証明書を新しい配布世代として登録 |
| `certpouch cert renew` | 既存のプライベートCAで対象証明書を発行・更新 |
| `certpouch cert status` | 公開ストア上の証明書の期限と状態を確認 |
| `certpouch dns fetch` | Google SheetsからDNS台帳を取得してキャッシュを更新 |
| `certpouch dns sync --dry-run` | DNS台帳から生成するNSD向けゾーンを確認 |
| `certpouch dns diff` | DNS候補と現行または基準のスナップショットを比較 |
| `certpouch dns apply` | 検証した候補を管理下のNSDへ適用 |

自動運用は `server.yaml` 1つで設定します。[DNSゾーン生成と適用](docs/dns-sync.md)・[証明書の登録手順](docs/server-publication.md)・[証明書の更新手順](docs/certificate-renewal.md)

## 仕様とライセンス

- [現行実装の全体仕様（英語）](SPEC.md)
- [詳細仕様書](docs/SPEC/README.md)
- [Taskでのビルド・テスト・リリース梱包](docs/release-and-acceptance.md#リリース候補)
- [DNS台帳の記入例](docs/dns-inventory.md#台帳の記入例)
- [相互TLSの設定](docs/mtls.md)
- [MITライセンス](LICENSE)
