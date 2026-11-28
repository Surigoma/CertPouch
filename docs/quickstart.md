# QuickStart

目的に合わせて、どちらかの手順から始めてください。

| 手順 | 向いている場面 |
| --- | --- |
| [A. Sheetsから自動運用を始める](#a-sheetsから自動運用を始める) | Sheetsにホストを登録するだけで、DNSと名前ごとの証明書を自動で反映したい |
| [B. 発行済み証明書を手動で配布する](manual-distribution.md) | 既存CAで発行済みの証明書を、既存の台帳を使って1台に配りたい |

Linuxへのバイナリ配置は[インストール手順](linux-installation.md)を参照してください。以下のドメイン、IP、サービス名、パスはすべて例です。

## A. Sheetsから自動運用を始める

まず試験用のシートにホストを1台登録し、CertPouchが読み取れることを確認します。後半でDNSに反映するときは、稼働中のNSD、既存のプライベートCA、配布API用のHTTPS証明書が必要です。

### A1. スプレッドシートを作る

1. 新しいスプレッドシートを開き、タブ名を `Servers` にします。
2. 下のタブ区切りの例をコピーし、セル **A1** に貼り付けます。
3. `192.0.2` がB2、`10` がC2、`web01` がD2、`portal` がG2に入っていることを確認します。

```text
除外	IPv4の先頭3区切り	IPv4の末尾	ホスト名	メモ1	メモ2	別名
	192.0.2	10	web01			portal
```

この例では、`192.0.2.10` に `web01.pvt.example.internal` と `portal.example.internal` が対応します。

- A列は通常空欄にし、除外する行だけ印を入れます。
- E・F列は自由欄です。空欄でも列は詰めません。
- 複数の別名は、同じG列のセルにカンマ区切りで書きます。
- 実運用では、IPとゾーン名を自分の環境の値に置き換えます。[台帳の列と記入例](dns-inventory.md#台帳の記入例)

ブラウザーのURLが `https://docs.google.com/spreadsheets/d/SPREADSHEET_ID/edit#gid=0` なら、`/d/` と次の `/` の間が **スプレッドシートID** です。読み取り範囲は、見出し行を除いて別名のG列までを含む `'Servers'!A2:G256` とします。

### A2. Google Sheets APIを有効にして鍵を作る

1. [Google Cloud Console](https://console.cloud.google.com/)でプロジェクトを作成するか、既存のプロジェクトを選びます。「APIとサービス」→「ライブラリ」で **Google Sheets API** を検索して有効にします。[Google公式のAPI有効化手順](https://developers.google.com/workspace/guides/enable-apis)
2. 「IAMと管理」→「サービス アカウント」→「サービス アカウントを作成」で、たとえば `certpouch-sheets` を作成します。シートを読むだけなら、編集者などのIAMロールは不要です。作成後、`名前@プロジェクトID.iam.gserviceaccount.com` 形式のメールアドレスを控えます。[Google公式のサービスアカウント作成手順](https://developers.google.com/workspace/guides/create-credentials)
3. 作成したアカウントを開き、「鍵」→「鍵を追加」→「新しい鍵を作成」で **JSON** を選びます。ダウンロードしたファイルが、CertPouchに渡す認証情報です。組織ポリシーで鍵の作成が禁止されている場合は、管理者に相談してください。[Google公式の鍵作成手順](https://docs.cloud.google.com/iam/docs/keys-create-delete)
4. Sheetsに戻って「共有」を開き、控えたメールアドレスを **閲覧者** として追加します。「一般的なアクセス」は「制限付き」のままで構いません。[Google公式の共有手順](https://support.google.com/drive/answer/7166529)

使うのはサービス アカウントのJSON鍵だけです。APIキー、OAuthクライアントID、ドメイン全体の委任は不要です。

### A3. LinuxにJSONを置き、Sheetsの読み取りを試す

JSONには秘密鍵が入っています。Git、チャット、共有フォルダーには置かず、安全な経路でLinuxへ転送してください。以下は、同期プロセスをrootで実行する同梱unitの例です。`/path/to/downloaded-key.json` は実際のパスに置き換えます。

```sh
sudo install -d -m 0700 /etc/certpouch/secrets
sudo install -m 0600 -o root -g root /path/to/downloaded-key.json /etc/certpouch/secrets/sheets.json
sudo install -d -m 0750 /var/lib/certpouch
```

[自動運用の設定例](../configs/server.example.yaml)を `/etc/certpouch/server.yaml` に置きます。まずは `inventory` と `dns` だけを使い、`certificates` と `distribution` は次の手順で追加します。Sheets関連は次のように編集します。サンプルにある他のタブは、実際に使うときだけ指定してください。

```yaml
inventory:
  cache_file: "/var/lib/certpouch/inventory.json"
  fallback_prefix: "192.0.2"
  poll_interval: "3m"
  sheets:
    spreadsheet_id: "REPLACE_WITH_SPREADSHEET_ID"
    ranges:
      - "'Servers'!A2:G256"
    credentials_file: "/etc/certpouch/secrets/sheets.json"
```

サンプル行を試す場合は `dns.zone` を `example.internal` にして、次を実行します。この段階では稼働中のゾーンは変わりません。serialは設定に書かなくても、プレビューはserial 1で表示されます。

```sh
sudo certpouch dns fetch --config /etc/certpouch/server.yaml
sudo certpouch dns sync --dry-run --config /etc/certpouch/server.yaml
```

- 1つ目のコマンドでホスト件数、2つ目で正引きと逆引きの候補を確認します。
- 取得できない場合は、APIを有効にしたプロジェクト、サービス アカウントへの共有、ID、タブ名、JSONのパスと権限を順に確認します。
- 複数のタブを使う場合は、`ranges` に `"'Containers'!A2:G128"` のように1行追加します。

### A4. NSDに反映して証明書を配る

#### 証明書・CAの早見表

CertPouchで扱う証明書は、**配布APIへのHTTPS接続に使うもの**と、**受け取り先のWebサービスが使うもの**の2系統です。

| ファイル | 何に使うか | 設定する場所 |
| --- | --- | --- |
| 配布APIのHTTPS証明書と秘密鍵（例 `server.pem`、`server.key`） | CertPouchの配布サーバーが提示する。SANはクライアントが接続する `server.url` のホスト名に合わせる | `server.yaml` の `distribution.tls.certificate_file`、`private_key_file` |
| 配布APIを信頼するCA証明書（例 `server-ca.crt`） | クライアントが接続先の配布APIを検証する | `client.yaml` の `server.ca_file` |
| 直接発行するCA証明書とCA秘密鍵（例 `issuer.pem`、`issuer.key`） | 既存CAで名前ごとの証明書に署名する。クライアントはCA証明書だけを使い、届いた証明書の直接発行者を検証する | `server.yaml` の `certificates.issuer.certificate_file`、`private_key_file`。CA証明書だけを `client.yaml` の `validation.issuer_certificate` にも指定 |
| 配布証明書を信頼するCAバンドル（例 `roots.pem`） | 発行時とPull時に、サービス用証明書のチェーンが信頼済みCAまでつながるか検証する | `server.yaml` の `certificates.trust_ca_file`、`client.yaml` の `validation.trust_ca_file` |
| 発行時に添付する中間CAチェーン（例 `chain.pem`） | 署名CAが中間CAなら、その証明書を先頭に上位の中間CAを並べる。ルートCAは含めない。ルートCAが直接署名する場合は省略 | `server.yaml` の `certificates.issuer.chain_file` |
| サービス用の証明書と秘密鍵 | CertPouchが台帳の名前ごとに発行し、Pull先のWebサービスなどが使う | `client.yaml` の `certificates[].name` で取得対象を選ぶ。サービスには配置先の `current/fullchain.pem` と `current/private-key.pem` を指定 |

- 配布APIが `cert-server.example.internal`、利用サービスが `portal.example.internal` なら、前者はクライアントの接続先、後者は取得する証明書名です。
- 双方を同じCAが署名している構成なら、信頼用のCAバンドルを共用できます。
- **CA秘密鍵は同期・発行を行うサーバーだけ**に置きます。
- A3のSheets用JSON鍵は、Google Sheetsにアクセスするための別の資格情報です。

#### 手順

1. 設定例の `dns.nsd`、`certificates`、`distribution` を実際の値で埋め、[専用NSD includeとディレクトリ](dns-sync.md)を準備します。
   - `certificates.targets` を書かずに `certificates.issuer` を設定すると、台帳上のすべての名前が発行対象になります。このとき、NSD反映前のキャッシュ更新を防ぐため `dns fetch` は使えません。以降の取得は `pipeline` で行います。
2. 初回同期の前に、配布サービスの実行ユーザーと公開ストアを用意します。同梱unitを使う場合は、`certpouch` ユーザーとグループを作成し、`certificates.store_dir` に指定したディレクトリをその所有で0700にします。Debian系の新規ホストで、設定例のパスを使う場合は次のとおりです。

   ```sh
   sudo useradd --system --user-group --no-create-home --shell /usr/sbin/nologin certpouch
   sudo install -d -m 0700 -o certpouch -g certpouch /var/lib/certpouch/certificates
   ```

   - A3で作ったキャッシュは同期ユーザーの所有とし、配布グループが読める0640にします。次の同期で、取得記録にもキャッシュと同じグループとモードが適用されます。親ディレクトリには配布ユーザーの通過権限を設定します。
   - 配布ユーザーが `server.yaml`、TLS鍵、トークンハッシュ、検証用CAを読めるようにします。署名CAの秘密鍵とSheets用JSON鍵は同期ユーザーだけが読めるようにします。[配布サーバーの権限](server-publication.md#systemdで配布サーバーを動かす)
3. 同期サービスを動かすユーザーで、ローカルの前提を確認してから初回同期を実行し、NSD反映と証明書発行を確認します。次は同梱の同期用unitと同じくrootで実行する例です。

   ```sh
   sudo certpouch pipeline --check --config /etc/certpouch/server.yaml
   sudo certpouch pipeline --once --config /etc/certpouch/server.yaml
   ```

4. 初回同期が成功したら、[同期用unit](../configs/systemd/certpouch-pipeline.service)と[配布用unit](../configs/systemd/certpouch-server.service)を `/etc/systemd/system/` に配置します。配布ユーザーで設定と公開ストアを検証してから起動します。

   ```sh
   sudo -u certpouch certpouch server --check-config --config /etc/certpouch/server.yaml
   sudo systemctl daemon-reload
   sudo systemctl enable --now certpouch-pipeline.service certpouch-server.service
   ```
5. 受け取り側で [client.yamlの例](../configs/client.example.yaml)を設定し、`certpouch pull --dry-run --config /etc/certpouch/client.yaml` で取得予定を確認します。

以降のホストの追加・変更はSheetsで行います。サーバー側の発行状況は `certpouch cert status --config /etc/certpouch/server.yaml` で確認できます。

## 次に読む

- 発行済み証明書を1台に手動で配る手順、普段使うコマンド、うまくいかないときの確認項目は[発行済み証明書を手動で配布する](manual-distribution.md)にあります。
- 受け取り側の権限、切り戻し、定期実行は[Linuxでの証明書配置](client-deployment.md)を参照してください。
- Sheets取得、NSD管理、CA更新の詳細は[Sheets取得](sheets-fetch.md)・[NSD管理](dns-sync.md)・[CA更新](certificate-renewal.md)を参照してください。
