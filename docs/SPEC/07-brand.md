# ブランド・ドキュメント仕様

## 基本情報

| 項目 | 内容 |
| --- | --- |
| 名称 | CertPouch |
| Tagline | Private certificates, delivered simply. |
| 分類 | DNS / Private PKI / Certificate Distribution |
| 方向性 | Friendly Infrastructure |
| 印象 | 親しみやすい、実用的、信頼できる、扱いやすい |
| ライセンス | MIT |

## 表現

小さく頼れるインフラツールとして表現する。

- **モチーフ:** ポーチまたは小さな容器、証明書、控えめなチェックまたはシールド。
- **避ける表現:** 複雑な南京錠、攻撃的なサイバー表現、過剰なグラデーションやネオン、企業CAベンダーに似た表現。

| 用途 | 色 |
| --- | --- |
| Primary / Deep Navy | `#142D40` |
| Accent / Soft Teal | `#39B8A5` |
| Background / Off White | `#F4F7F7` |
| Secondary / Slate Blue | `#A8C7D2` |
| Text / Dark Slate | `#263746` |

- フォントはInterを第一候補、system sans-serifを代替とする。
- ワードマークはMediumまたはSemiboldを使い、過度な装飾書体を避ける。

## ブランド資産

| パス | 役割 |
| --- | --- |
| `assets/logo.svg` | 基本ロゴ・README向けの横組み |
| `assets/logo-dark.svg` | ダークテーマ向け |
| `assets/logo-light.svg` | ライトテーマ向け |
| `assets/icon.svg` | 正方形のアイコン原本 |
| `assets/icon-512.png` | 512×512のラスターアイコン |
| `assets/social-preview.svg`、`assets/social-preview.png` | 1200×630のソーシャルプレビュー |

- SVGを一次ソースとし、アイコンの背景は透過する。
- 独自の図案とし、第三者の商標を取り込まない。

## README・公開説明

- READMEでは、目的、DNS/PKI/配布/配置の機能、構成、CLI、セキュリティ、設計原則、運用受け入れ、MITライセンスを案内する。
- 日英のREADMEから、初回導入ガイドと詳細仕様にリンクする。

## 文書の書き方

- 利用者向けの手順書（`docs/*.md`）は「です・ます」、仕様書（`docs/SPEC/`、`docs/HANDOFF.md`）は「である」で書く。
- 各文書の冒頭で、目的と前提を短く示す。
- 1段落に詰め込まず、手順は番号付きリスト、条件や項目の対応は表、並列の事項は箇条書きにする。
- 1つの文書は話題単位で300行以内に保つ。
