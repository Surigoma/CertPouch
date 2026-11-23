# 証明書配布API仕様

## 要件

HTTPSのバージョン付きAPIとし、認証、送信元IP制限、証明書名ごとの認可を行う。IPは暗号学的な主体識別の代替にしない。接続元IPが台帳または静的設定で許可された証明書だけを配布する。CA秘密鍵は一切配布しない。

メタデータには名前、SHA-256 fingerprint、NotBefore/NotAfter、Subject、SAN、Issuer、versionを含める。ダウンロードではチェーンと秘密鍵を明確に区別する。

## 詳細化案：HTTP共通契約

- TLS最小バージョンは1.2。クライアントはホスト名とCAを検証し、検証を無効化しない。
- 認証は`Authorization: Bearer <token>`と`X-CertPouch-Client-ID`を使う。資格情報をURLへ渡さない。
- health以外の全APIで認証・IP制限を必須にする。メタデータも公開しない。
- IPはTCP接続元から判定し、IPv4-mapped IPv6を正規化する。`Forwarded`や`X-Forwarded-For`を信頼しない。Phase 1は直接接続を前提とする。
- クライアントIDで設定を選択し、enabledとトークンハッシュを検証する。ハッシュの比較には定数時間比較を用いる。
- DNS台帳モードでは証明書の論理名に対応するIPと、該当クライアントの例外設定にあるホストのIPから、接続元に許可する証明書を決める。静的モードではクライアントの許可IPと証明書割り当てを使う。
- リダイレクトには追従せず失敗とする。資格情報を別originに転送しない。
- JSONはUTF-8、日時はUTCのRFC 3339、fingerprintとSHA-256は小文字16進64桁とする。
- 全応答に`Cache-Control: no-store`を付け、秘密鍵をキャッシュさせない。
- クライアント要求全体のタイムアウトは60秒、サーバーReadHeaderTimeoutは5秒、WriteTimeoutは60秒を初期案とする。
- 一つのダウンロード応答の上限は4 MiB（JSONエンコード後）とし、クライアントでもストリーム読み取りに上限を設ける。
- 一時障害時の同一実行内自動リトライはPhase 1では行わず、次回timer実行で再試行する。

## エンドポイント

| GETパス | 成功 | 内容 |
| --- | --- | --- |
| `/api/v1/health` | 200 | `{"status":"ok"}`のみ。秘密・在庫を含めない |
| `/api/v1/certificates` | 200 | 許可済み証明書のメタデータを`certificates`配列で返す |
| `/api/v1/certificates/{name}/metadata` | 200 | 現在公開中の世代のメタデータ |
| `/api/v1/certificates/{name}/download?version={version}` | 200 | 指定した不変世代の一式 |

詳細化案として、downloadにはversionを必須にする。URLに含めるのは非秘密の識別子だけとする。name/versionはデコード後に検証し、パス区切り、二重デコード、ディレクトリトラバーサルを拒否する。

一覧は名前の昇順で返す。権限があっても未公開の証明書は一覧に含めない。空一覧は正常な200とする。許可された全在庫を返すが、クライアントはローカル設定との積集合だけを配置する。

### メタデータ例

以下の`<...>`は型を説明するプレースホルダーであり、実際の値ではない。

```json
{
  "name": "web.example.internal",
  "version": "<opaque-version-id>",
  "fingerprint": "<64-lowercase-hex>",
  "not_before": "2026-11-01T00:00:00Z",
  "not_after": "2027-02-01T00:00:00Z",
  "subject": "CN=web.example.internal",
  "dns_sans": ["web.example.internal"],
  "ip_sans": [],
  "issuer": "CN=Example Intermediate CA",
  "artifact_sha256": {
    "certificate.pem": "<64-lowercase-hex>",
    "chain.pem": "<64-lowercase-hex>",
    "private-key.pem": "<64-lowercase-hex>"
  }
}
```

`certificate.pem`はリーフのみ、`chain.pem`は直接発行中間CAから上位中間CAの順（ルートは除外）、`private-key.pem`は対応する秘密鍵とする。直接ルート署名の場合、chainは空を許容する。

### ダウンロード形式

詳細化案として、アーカイブ展開によるパス・権限問題を避けるためJSONを採用する。

```json
{
  "metadata": { "name": "web.example.internal", "version": "<opaque-version-id>" },
  "artifacts": {
    "certificate.pem": "<PEM string>",
    "chain.pem": "<PEM string>",
    "private-key.pem": "<PEM string>"
  }
}
```

実応答のmetadataは前項の全フィールドを含む。artifactのハッシュ対象はJSONデコード後のPEM文字列のUTF-8バイト列とし、改行を正規化せず照合する。期待した3キー以外は拒否する。応答のname/versionと要求を照合し、取得済みメタデータとハッシュが変わった場合は配置しない。

サーバーは一要求につき一つの不変世代を読み、途中で公開世代が変わっても異なる世代の鍵と証明書を混ぜない。指定世代が削除済みなら409で終了し、次回Pullでメタデータから取り直す。

ハッシュは整合性検査であり、配布サーバー侵害への独立した署名保証ではない。TLSとローカルCA/SAN/発行者検証を併用する。

## エラー契約（詳細化案）

| HTTP | 条件 | code |
| --- | --- | --- |
| 400 | 名前・version・入力形式不正 | `invalid_request` |
| 401 | 認証欠落、不正トークン、無効・未知ID、静的モードでの接続元IP拒否 | `unauthorized` |
| 404 | 証明書が存在しない、または接続元に取得の許可がない | `not_found` |
| 405 | GET以外 | `method_not_allowed` |
| 409 | 要求世代が取得不能 | `version_unavailable` |
| 500 | 保存領域読み出し等の内部障害 | `internal_error` |
| 503 | healthで保存領域を利用できない | `unavailable` |

応答は`{"error":{"code":"...","message":"..."}}`とし、パス、秘密情報、内部スタックを含めない。認可前に証明書の存在を判別できる応答を返さない。healthはHTTPサーバーと保存領域の利用可否を示し、証明書在庫や全証明書の有効性は保証しない。

DNS台帳モードで有効なトークンを持つ接続元に許可された証明書がない場合、一覧は200と空配列を返す。個別のメタデータ取得とダウンロードは404とする。`server.inventory_refresh` を有効にすると新しい台帳の認可を定期的に反映する。台帳の再読込に失敗した場合は正常な台帳を読み込むまで認証を停止し、401を返す。`dns_inventory.max_age` を設定した場合、取得記録に基づく台帳の有効期限を新規リクエストごとに確認し、期限切れは401とする。定期読み込みを省略した場合は再起動時に台帳の変更を反映する。

資格情報の無効化は設定再起動以後の新規要求に適用する。既に取得された秘密鍵は回収できず、必要に応じて鍵・証明書の交換が必要である。mTLSを有効化すると全HTTPS接続でクライアント証明書の検証が必要になり、配布操作ではリーフのフィンガープリントをクライアントIDに結び付ける。[mTLSの設定](../mtls.md) リバースプロキシ経由の信頼IP判定は後続設計とする。
