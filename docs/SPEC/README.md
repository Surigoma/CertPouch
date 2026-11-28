# CertPouch 詳細仕様書

[全体仕様](../../SPEC.md)の各章を詳しく定める。

- **現行実装:** 現在利用できる設定・API・運用上の契約。下表の01〜07章。
- **目標仕様:** 台帳からDNS・証明書を自動同期する将来の運用。[08章](08-inventory-workflow.md)。
- **最初に試す場合:** [QuickStart](../quickstart.md)。

## 現行実装

| 文書 | 内容 |
| --- | --- |
| [01 全体構成とCLI](01-scope-architecture.md) | データの流れ、パッケージの責務、コマンド |
| [02 設定仕様](02-configuration.md) | サーバー・クライアントのYAML、認証情報、台帳 |
| [03 証明書配布API](03-distribution-api.md) | HTTPS API、認証・認可、通信形式、エラー |
| [04 証明書の検証と配置](04-certificate-deployment.md) | 公開ストア、検証、変更検出、配置、切り戻し |
| [05 DNSとプライベートPKI](05-dns-pki.md) | 台帳、NSD候補、serial、発行と更新 |
| [06 検証と運用](06-verification-operations.md) | 試験条件、ログと同期状態、導入時の確認 |
| [07 ブランド資産](07-brand.md) | ロゴ、アイコン、ソーシャルプレビュー |

## 目標仕様

| 文書 | 内容 |
| --- | --- |
| [08 台帳からの同期](08-inventory-workflow.md) | Sheets登録だけでDNS・証明書を反映する運用、取得制限、削除時の扱い |

## 運用手順

| 目的 | 手順 |
| --- | --- |
| 初回設定、証明書の登録と配布 | [QuickStart](../quickstart.md)・[手動配布](../manual-distribution.md)・[サーバー](../server-publication.md)・[クライアント](../client-deployment.md) |
| CLIで初期設定を生成 | [設定コマンド](../configure.md) |
| 利用シーン別のコマンドと設定例 | [操作例](../user-flows.md) |
| Linuxへのバイナリ配置と削除 | [インストールと削除](../linux-installation.md) |
| 接続元の制限とクライアント管理 | [DNS台帳による認可](../dns-inventory.md)・[mTLS](../mtls.md)・[失効リスト](../client-revocation.md) |
| 台帳取得とNSD管理 | [Sheetsからの取得](../sheets-fetch.md)・[NSDへの適用](../dns-sync.md) |
| 既存CAでの発行・更新 | [証明書更新](../certificate-renewal.md) |
| 運用前の検証とリリース | [受け入れ手順](../release-and-acceptance.md)・[検証結果と引き継ぎ](../HANDOFF.md) |

## 共通用語

| 用語 | 意味 |
| --- | --- |
| 台帳 | ホスト名・IP・別名を記録したDNS台帳。Google Sheets、そのJSONキャッシュ、またはCSV |
| 証明書名 | 配布認可と設定に使う論理識別子。TLSの名前検証に使うSANは別に設定する |
| クライアントID | 証明書を取得するクライアントの認証主体 |
| 世代 | リーフ証明書・チェーン・秘密鍵をまとめた不変の単位 |
| フィンガープリント | リーフ証明書DERのSHA-256 |
| 公開 | 検証済み世代をサーバーの配布対象にする操作 |
| 配置 | クライアントの `current` を検証済み世代へ切り替え、利用サービスへ反映する操作 |
| 候補 | `dns sync` が生成し、`nsd-checkzone` で検証したゾーンファイル一式 |
