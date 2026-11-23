# CertPouch

[English](README.md)

**Private certificates, delivered simply.**

CertPouchは、プライベートDNS、プライベートPKI、Pull型の証明書配布を扱うセルフホスト型のプロジェクトです。既存のプライベートCAを利用し、単一のGoバイナリで動作する設計です。

**開発状況：** HTTPS配布サーバー、Pullの更新プレビュー、Linuxとsystemdを使った配置を実装済みです。[Linuxでの配置手順](docs/client-deployment.md)

## 仕組み

中央サーバーで証明書と秘密鍵を保管します。DNS台帳モードでは、認証済みホストの接続元IPがGoogle Sheetsのスナップショット上で証明書名に対応するIPと一致すると取得できます。別ホストからの取得も例外設定で許可できます。今後実装するPullクライアントは、証明書を検証してから新しい世代に切り替え、サービス操作に失敗した場合は切り戻します。[DNS台帳の設定](docs/dns-inventory.md)

## ロードマップ

1. **証明書配布。** HTTPS、クライアント認証、接続元IPの許可リスト、証明書単位の認可、SHA-256による差分検出、安全な配置を実装します。最初の連携対象はLinux上のKeycloak 1ホストです。
2. **プライベートDNS。** Google SheetsからDNS台帳を読み取り、NSD向けの正引き・逆引きゾーンを生成、検証、適用します。
3. **プライベートPKI。** 既存のプライベートCAでサーバー証明書を発行・更新し、検証済みの世代を公開します。
4. **本番運用への準備。** 監視を改善し、mTLSを追加して、認証情報の失効やリリースの手順を整備します。

## CLI

| コマンド | 用途 |
| --- | --- |
| `certpouch server` | HTTPS配布サーバーを起動 |
| `certpouch pull` | 証明書の更新を取得して配置 |
| `certpouch pull --dry-run` | 更新予定を確認 |
| `certpouch status` | ローカルの証明書と配置状況を確認 |
| `certpouch dns sync` | DNSゾーンを同期 |
| `certpouch cert renew` | 更新時期を迎えた証明書を更新 |

`server`、`pull`、`status` には `--config PATH` が必要です。DNS同期と証明書更新は実装予定です。[クライアント設定例](configs/client.example.yaml)

## 仕様とライセンス

- [プロジェクト仕様（英語）](SPEC.md)
- [詳細仕様書](docs/SPEC/README.md)
- [MITライセンス](LICENSE)
