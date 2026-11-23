# ブランド・ドキュメント仕様

## 要件

| 項目 | 内容 |
| --- | --- |
| 名称 | CertPouch |
| Tagline | Private certificates, delivered simply. |
| 分類 | DNS / Private PKI / Certificate Distribution |
| 方向性 | Friendly Infrastructure |
| 印象 | 親しみやすい、実用的、信頼できる、扱いやすい |
| ライセンス | MIT |

小さく頼れるインフラツールとして表現する。ロゴはポーチまたは小さな容器、証明書、控えめなチェックまたはシールドをモチーフとする。複雑な南京錠、攻撃的なサイバー表現、過剰なグラデーションやネオン、企業CAベンダーに似た表現を避ける。

| 用途 | 色 |
| --- | --- |
| Primary / Deep Navy | `#142D40` |
| Accent / Soft Teal | `#39B8A5` |
| Background / Off White | `#F4F7F7` |
| Secondary / Slate Blue | `#A8C7D2` |
| Text / Dark Slate | `#263746` |

フォントはInterを第一候補、system sans-serifを代替とする。ワードマークはMediumまたはSemiboldを使い、過度な装飾書体を避ける。

## ブランド資産

| パス | 役割 |
| --- | --- |
| `assets/logo.svg` | 基本ロゴ・README向け横組み |
| `assets/logo-dark.svg` | ダークテーマ向け |
| `assets/logo-light.svg` | ライトテーマ向け |
| `assets/icon.svg` | 正方形のアイコン原本 |
| `assets/icon-512.png` | 512×512のラスターアイコン |
| `assets/social-preview.svg`, `assets/social-preview.png` | 1200×630のソーシャルプレビュー |

SVGを一次ソースとし、アイコンは背景を透過する。独自の図案とし、第三者商標を取り込まない。

## README・公開説明

READMEでは目的、DNS/PKI/配布/配置の機能、構成、CLI、セキュリティ、設計原則、ロードマップ、MITライセンスを案内する。計画中の機能を実装済みとして記載しない。CLIと設定は初期開発中に変更される可能性を示す。
