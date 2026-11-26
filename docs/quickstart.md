# QuickStart — SheetsからDNSと証明書を管理する

## Sheetsから自動運用を始める

まず、試験用のシートにホストを1台登録し、CertPouchが読み取れることを確認します。Linuxへのバイナリ配置は[インストール手順](linux-installation.md)を参照してください。後半でDNSに反映するときは、稼働中のNSDと既存のプライベートCA、配布API用のHTTPS証明書が必要です。

### 1. スプレッドシートを作る

新しいスプレッドシートを開いて、タブ名を `Servers` にします。下のタブ区切りの例をコピーし、セル **A1** に貼り付けます。貼り付け後、`192.0.2` がB2、`10` がC2、`web01` がD2、`portal` がG2に入っていることを確認します。

```text
除外	IPv4の先頭3区切り	IPv4の末尾	ホスト名	メモ1	メモ2	別名
	192.0.2	10	web01			portal
```

この例では `192.0.2.10` に `web01.pvt.example.internal` と `portal.example.internal` が対応します。A列は通常空欄にし、除外する行だけ印を入れます。E・F列は自由欄で、空欄でも列は詰めません。複数の別名は同じG列のセルにカンマ区切りで書きます。実運用ではIPとゾーン名を自分の環境の値に置き換えます。[台帳の列と記入例](dns-inventory.md#台帳の記入例)

ブラウザーのURLが `https://docs.google.com/spreadsheets/d/SPREADSHEET_ID/edit#gid=0` なら、`/d/` と次の `/` の間が **スプレッドシートID** です。この例の読み取り範囲は `'Servers'!A2:G256` です。先頭行の見出しを除き、別名が入るG列までを指定します。

### 2. Google Sheets APIを有効にして鍵を作る

1. [Google Cloud Console](https://console.cloud.google.com/)でプロジェクトを作成するか、既存のプロジェクトを選びます。同じプロジェクトで「APIとサービス」→「ライブラリ」を開き、**Google Sheets API** を検索して有効にします。[Google公式のAPI有効化手順](https://developers.google.com/workspace/guides/enable-apis)
2. 「IAMと管理」→「サービス アカウント」→「サービス アカウントを作成」で、たとえば `certpouch-sheets` を作成します。このシートの読み取りだけなら、サービス アカウントにプロジェクトの編集者などのIAMロールを付与する必要はありません。作成後、`名前@プロジェクトID.iam.gserviceaccount.com` 形式のメールアドレスを控えます。[Google公式のサービスアカウント作成手順](https://developers.google.com/workspace/guides/create-credentials)
3. 作成したアカウントを開き、「鍵」→「鍵を追加」→「新しい鍵を作成」で **JSON** を選びます。ダウンロードされたファイルがCertPouchに渡す認証情報です。組織ポリシーで鍵の作成が禁止されている場合は、管理者に相談してください。[Google公式の鍵作成手順](https://docs.cloud.google.com/iam/docs/keys-create-delete)
4. Sheetsに戻って「共有」を開き、控えたサービス アカウントのメールアドレスを **閲覧者** として追加します。「一般的なアクセス」は「制限付き」のまま使えます。[Google公式の共有手順](https://support.google.com/drive/answer/7166529)

CertPouchがこの手順で使うのはサービス アカウントのJSON鍵です。APIキーやOAuthクライアントIDの作成、ドメイン全体の委任は不要です。

### 3. LinuxにJSONを置き、Sheetsの読み取りを試す

JSONには秘密鍵が入っています。Git、チャット、共有フォルダーには置かず、安全な経路でLinuxへ転送してください。以下は、同期プロセスをrootで実行する同梱unitの例です。`/path/to/downloaded-key.json` は転送したファイルの実パスに置き換えます。

```sh
sudo install -d -m 0700 /etc/certpouch/secrets
sudo install -m 0600 -o root -g root /path/to/downloaded-key.json /etc/certpouch/secrets/sheets.json
sudo install -d -m 0750 /var/lib/certpouch
```

[自動運用の設定例](../configs/workflow-server.example.yaml)を `/etc/certpouch/server.yaml` に置きます。まず `inventory` と `dns` を使い、`certificates` と `distribution` は次の手順で追加します。Sheets関連は以下のように編集します。サンプルにある他のタブは、実際に使うときだけ指定してください。

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

先ほどのサンプル行を試す場合は `dns.zone` を `example.internal` とし、個別プレビュー用に `dns.zones.serial: 1` を追加します。次を実行します。この段階では稼働中のゾーンに反映されません。

```sh
sudo certpouch dns fetch --config /etc/certpouch/server.yaml
sudo certpouch dns sync --dry-run --config /etc/certpouch/server.yaml
```

最初のコマンドでホスト件数、次のコマンドで正引きと逆引きの候補を確認します。取得できなければ、APIを有効にしたプロジェクト、サービス アカウントへの共有、ID、タブ名、JSONのパスと権限を順に確認します。複数タブを使う場合は、同じ `ranges` リストに `"'Containers'!A2:G128"` のように1行追加します。

### 4. NSDに反映して証明書を配る

#### 証明書・CAの早見表

CertPouchには、**配布APIへのHTTPS接続を確認する証明書**と、**受け取り先のWebサービスが使う証明書**があります。設定例では、それぞれ次のファイルを指定します。

| ファイル | 何に使うか | 設定する場所 |
| --- | --- | --- |
| 配布APIのHTTPS証明書と秘密鍵（例 `server.pem` と `server.key`） | CertPouchのHTTPS配布サーバーが提示する。証明書のSANは、クライアントが接続する `server.url` のホスト名に合わせる | `server.yaml` の `distribution.tls.certificate_file` と `private_key_file` |
| 配布APIを信頼するCA証明書（例 `server-ca.crt`） | クライアントが接続先の配布APIを検証する | `client.yaml` の `server.ca_file` |
| 直接発行するCA証明書とCA秘密鍵（例 `issuer.pem` と `issuer.key`） | 既存CAでホスト名や別名ごとの証明書に署名する。クライアントは公開情報であるCA証明書を使い、届いた証明書の直接発行者も検証する | `server.yaml` の `certificates.issuer.certificate_file` と `private_key_file`。CA証明書だけを `client.yaml` の `validation.issuer_certificate` に指定 |
| 配布証明書を信頼するCAバンドル（例 `roots.pem`） | 発行時とPull時に、サービス用証明書のチェーンが信頼済みCAまでつながるか検証する | `server.yaml` の `certificates.trust_ca_file`、`client.yaml` の `validation.trust_ca_file` |
| 発行時に添付する中間CAチェーン（例 `chain.pem`） | 署名CAが中間CAなら、その証明書を先頭に上位の中間CA証明書を並べる。ルートCAは含めず、ルートCAが直接署名する場合は省略する | `server.yaml` の `certificates.issuer.chain_file` |
| ホストや別名ごとのサービス用証明書と秘密鍵 | CertPouchが台帳の名前ごとに発行し、Pull先のWebサービスなどが使用する | `client.yaml` の `certificates[].name` で取得対象を選ぶ。利用サービスには配置先の `current/fullchain.pem` と `current/private-key.pem` を指定 |

たとえば配布APIが `cert-server.example.internal`、利用サービスが `portal.example.internal` なら、前者はクライアントの接続先、後者は取得する証明書名です。双方を同じCAが署名している構成なら、信頼用の公開CAバンドルを共用できます。**CA秘密鍵は同期・発行を行うサーバー側だけ**に置きます。手順3のSheets用JSON鍵は、Google Sheetsにアクセスするための資格情報です。

1. 試験用の値を実運用の値に置き換え、プレビュー用の固定 `serial` を外します。設定例の `dns.nsd`、`certificates`、`distribution` を埋め、[専用NSD includeとディレクトリ](dns-sync.md)を準備します。同じキャッシュを使う場合は、同期ユーザーが所有し、配布ユーザーが読める権限を設定します。
2. 同期サービスを動かすユーザーで `certpouch pipeline --check --config /etc/certpouch/server.yaml` を実行します。これはローカルの前提確認です。次に `certpouch pipeline --once --config /etc/certpouch/server.yaml` を実行して、NSD反映と証明書発行を確認します。
3. 初回同期が成功したら、[同期用unit](../configs/systemd/certpouch-pipeline.service)と[配布用unit](../configs/systemd/certpouch-server.service)で常駐させます。受け取り側は [client.yamlの例](../configs/client.example.yaml)を設定し、`certpouch pull --dry-run --config /etc/certpouch/client.yaml` で取得予定を確認します。

以降のホスト追加・変更はSheetsで行います。サーバー側の発行状況は `certpouch cert status --config /etc/certpouch/server.yaml` で確認できます。

## 発行済み証明書を手動で配布する

既存CAで発行済みの証明書を、既存のDNS台帳を使ってLinuxホスト1台へ配布する手順です。配布サーバーと取得ホストは別マシンとし、サーバーには `server.yaml`、取得ホストには `client.yaml` を置きます。以下のドメイン、IP、サービス名、ファイルパスは例です。

サーバー側では配布、Sheets取得、DNS生成、証明書更新を `server.yaml` にまとめ、取得ホスト側は `client.yaml` を使う。[設定コマンド](configure.md)

## 1. 用意するもの

| 用意するもの | 入手先・置き場所 |
| --- | --- |
| CertPouchのLinuxバイナリ | 両ホストの `/usr/local/bin/certpouch` に配置 |
| 配布サーバー用TLS証明書と秘密鍵 | 配布サーバーに配置。証明書のSANは、取得ホストが接続する名前（例 `cert-server.example.internal`）に合わせる。取得ホストにはこの証明書を信頼するCAを置く |
| 配布対象のリーフ証明書、秘密鍵、必要な中間証明書 | 既存の発行手順から取得。配布サーバーの管理者だけが読める場所に置く |
| 配布対象の発行者証明書と信頼CA | 発行者は配布対象のリーフへ直接署名したCA。発行者証明書と信頼CAを両ホストに配置 |
| 既存のDNS台帳 | Google Sheetsの取得結果を保存したJSONキャッシュ、またはCSVを配布サーバーへ置く。証明書の論理名と一致するレコードを確認する |
| 取得ホストの利用サービス | 証明書を使うsystemdユニット。例は `nginx.service` |

配布サーバー自身のTLS用CAと、配布対象の証明書を検証するCAは用途が別です。既存構成で両方に同じCAを使用している場合は同じバンドルを参照できます。

### `configure` で初期設定を作る

Linux上で、CAファイル、サーバーのHTTPS証明書と秘密鍵、台帳を先に用意します。以下の `--out` は新規ディレクトリを指定します。親ディレクトリは事前に作成してください。

取得ホストで、トークンとクライアント設定を作成します。

```sh
sudo certpouch configure client --out /etc/certpouch/client-init \
  --id web-server-01 --name web.example.internal \
  --url https://cert-server.example.internal:8443 \
  --server-ca /etc/ssl/certpouch/server-ca.crt \
  --trust-ca /etc/ssl/certpouch/issuers.pem \
  --issuer /etc/ssl/certpouch/intermediate.crt --unit nginx.service
```

生成された `client.token.sha256` だけを配布サーバーの管理者へ安全に渡します。配布サーバーで、受け取ったハッシュと台帳を指定して設定を作成します。

```sh
sudo certpouch configure server --out /etc/certpouch/server-init \
  --id web-server-01 --name web.example.internal \
  --listen 192.0.2.20:8443 \
  --tls-cert /etc/certpouch/server.crt --tls-key /etc/certpouch/server.key \
  --trust-ca /etc/certpouch/issuers.pem --issuer /etc/certpouch/intermediate.crt \
  --token-hash /secure/drop/client.token.sha256 \
  --csv /var/lib/dns-inventory/export.csv \
  --zone example.internal --fallback-prefix 192.0.2
```

JSONの台帳キャッシュを使う場合は `--csv` の代わりに `--cache` を指定します。DNS SANは証明書名が既定です。異なる場合は両コマンドに `--dns-san` を指定します。設定と資格情報は0600で作成され、既存の出力先には上書きしません。

この例の公開ストアは `/etc/certpouch/server-init/publications`、クライアントの配置先は `/etc/certpouch/client-init/deployment` です。下記コマンド例の `/etc/certpouch/server.yaml` と `/etc/certpouch/client.yaml` は、それぞれ `/etc/certpouch/server-init/server.yaml` と `/etc/certpouch/client-init/client.yaml` に置き換えます。systemdユニットの `--config` も同じパスに変更します。

生成ディレクトリは0700、設定と資格情報は0600です。実行ユーザーが初期化を行ったユーザーと異なる場合は、下記の権限手順に従って読取・通過権限を設定します。公開ストアは配布サーバーの専用ユーザーがアクセスできるようにします。初期化後は手順3の証明書登録と起動へ進みます。手順2と手順3・4のYAML手作業は省略できます。

この例のようにrootでサーバーを初期化し、unitを `certpouch` ユーザーで実行する場合は、専用ユーザーを作成したあと、配布サーバーで次を実行します。

```sh
sudo chgrp certpouch /etc/certpouch/server-init /etc/certpouch/server-init/server.yaml
sudo chmod 0750 /etc/certpouch/server-init
sudo chmod 0640 /etc/certpouch/server-init/server.yaml
sudo chown certpouch:certpouch /etc/certpouch/server-init/client.token.sha256 \
  /etc/certpouch/server-init/publications
```

サーバー用のTLS秘密鍵、CA、台帳は `init` がコピーしないため、参照先の権限も確認します。

## 2. 手動でクライアント資格情報を作る

信頼できる管理端末のBashで、クライアントごとに別のトークンを作ります。次の処理はトークンを画面に表示しません。

```bash
set -euo pipefail
umask 077
credentials_dir=$(mktemp -d)
openssl rand 32 | openssl base64 -A | tr '/+' '_-' | tr -d '=' > "$credentials_dir/client.token"
sha256sum "$credentials_dir/client.token" | awk '{print $1}' > "$credentials_dir/web-server-01.sha256"
```

`client.token` を安全な経路で取得ホストの `/etc/certpouch/client.token` へ、`web-server-01.sha256` を配布サーバーの `/etc/certpouch/credentials/web-server-01.sha256` へ配置します。両ファイルを0600に設定し、前者はPull実行ユーザー、後者は配布サーバー実行ユーザーが読めるようにします。配布サーバーが使うのはハッシュです。

## 3. 配布サーバーを設定して起動する

[サーバー設定例](../configs/server.example.yaml)を `/etc/certpouch/server.yaml` にコピーし、次の値を設定します。

| YAMLキー | 設定する値 |
| --- | --- |
| `server.listen` | 取得ホストから到達できるサーバー側IPとポート。設定例の `127.0.0.1:8443` は同じホストからの接続用。別ホストから接続する場合は内部IPなどに変更 |
| `server.tls.certificate`, `server.tls.private_key` | 配布サーバー自身のHTTPS証明書と秘密鍵の絶対パス |
| `storage.certificate_dir`, `storage.trust_ca_file` | 公開世代の保存先と、配布対象を検証するCAバンドル |
| `dns_inventory.cache_file` | 台帳のJSONキャッシュへの絶対パス。CSVの場合は代わりに `csv_files: ["/path/to/inventory.csv"]` を設定 |
| `dns_inventory.zone`, `dns_inventory.fallback_prefix` | 台帳上の名前に補うゾーン名と、台帳でプレフィックスが指定される前に使うIPv4プレフィックス。[台帳の記入例](dns-inventory.md#台帳の記入例) |
| `certificates[0].name`, `expected_dns_sans`, `issuer_certificate` | 台帳の完全修飾名（例 `web.example.internal`）、リーフに期待するDNS SAN、直接発行者の証明書 |
| `clients[0].id`, `enabled`, `token_hash_file` | クライアントID（例 `web-server-01`）、`true`、手順2で置いたハッシュの絶対パス |

台帳モードでは、配布サーバーから見た取得ホストの接続元IPを、証明書の論理名に対応する台帳のIPと照合します。別ホストから取得させる場合は、そのホスト名を台帳に登録し、`certificate_access_overrides` に追加します。[例外の設定](dns-inventory.md)

専用ユーザーと0700の公開ストアを作り、サーバー実行ユーザーが設定、台帳、TLS秘密鍵、トークンハッシュを読めるようにします。台帳は実行ユーザー以外が書き込める場所に置かないでください。サーバー用systemdユニットは[サーバーの導入手順](server-publication.md#systemdで配布サーバーを動かす場合)に従って配置します。

例えばDebian系の新規検証ホストでは、専用ユーザーが未作成の場合に次を実行します。

```sh
sudo useradd --system --user-group --no-create-home --shell /usr/sbin/nologin certpouch
sudo install -d -m 0700 -o certpouch -g certpouch /var/lib/certpouch/certificates
```

設定ファイルを管理者が管理する場合、`server.yaml` は `root:certpouch` の0640、サーバーのTLS秘密鍵とトークンハッシュは `certpouch` が読める0600を使います。ファイルに加え、親ディレクトリの通過権限も確認します。

配布サーバーで、対象証明書を登録してから起動します。

```sh
sudo certpouch cert publish --config /etc/certpouch/server.yaml \
  --name web.example.internal \
  --certificate /secure/drop/leaf.pem \
  --chain /secure/drop/intermediates.pem \
  --private-key /secure/drop/private-key.pem
sudo -u certpouch certpouch server --check-config --config /etc/certpouch/server.yaml
sudo systemctl daemon-reload
sudo systemctl enable --now certpouch-server.service
```

リーフが信頼済みルートCAから直接発行されている場合は `--chain` を省略します。登録が成功すると証明書名と世代IDが表示されます。

## 4. 取得ホストを設定して初回Pullを実行する

[クライアント設定例](../configs/client.example.yaml)を `/etc/certpouch/client.yaml` にコピーし、次を設定します。

| YAMLキー | 設定する値 |
| --- | --- |
| `server.url`, `server.ca_file` | 配布サーバーのHTTPS URLと、そのTLS証明書を信頼するCA。URLのホスト名はサーバー証明書のSANと一致させる |
| `client.id`, `client.token_file` | サーバーのクライアントIDと一致する値、手順2で配置したトークンの絶対パス |
| `validation.trust_ca_file` | 配布対象を検証するCAバンドル |
| `certificates[0].name`, `expected_dns_sans`, `issuer_certificate` | サーバー側の配布対象名・SAN・直接発行者と一致する設定 |
| `certificates[0].destination` | 配置先の絶対パス。事前に作成し、Pull実行ユーザーに書込権限を与える |
| `certificates[0].service.unit`, `action` | 対象のsystemdユニット名と `reload` または `restart`。停止中のサービスを初回配置後に起動する構成では `restart` を指定 |

本番サービスの参照先を変える前に、まず取得ホストから到達性と認可を確認します。

```sh
curl --fail --cacert /etc/ssl/certpouch/server-ca.crt \
  https://cert-server.example.internal:8443/api/v1/health
sudo certpouch pull --dry-run --config /etc/certpouch/client.yaml
```

healthの `{"status":"ok"}` はHTTPS接続を確認するものです。dry-runに対象名・`install`・世代IDが出れば、トークン認証と接続元認可も通過しています。

既存のサービス設定を退避し、利用サービスの証明書と秘密鍵の参照先を、配置先以下の `current/fullchain.pem` と `current/private-key.pem` に設定します。Pullと別ユーザーで動くサービスには、必要なグループ権限を設定します。[配置先の権限と切り戻し](client-deployment.md)

```sh
sudo certpouch pull --config /etc/certpouch/client.yaml
sudo certpouch status --config /etc/certpouch/client.yaml
```

statusの対象証明書が `installed` になり、`last_successful_sync` が表示されることを確認します。利用サービスにもTLS接続して配信証明書を確認します。手動Pullを確認してから[定期実行](client-deployment.md)へ進みます。

## 普段使うコマンド

`init` を使った場合のパスを示します。手動で設定した場合は、自分のYAMLのパスに置き換えてください。

| 実行する場所・目的 | コマンド |
| --- | --- |
| 取得ホストで更新予定を見る | `sudo certpouch pull --dry-run --config /etc/certpouch/client-init/client.yaml` |
| 取得ホストで更新を反映する | `sudo certpouch pull --config /etc/certpouch/client-init/client.yaml` |
| 取得ホストで配置状態を確認する | `sudo certpouch status --config /etc/certpouch/client-init/client.yaml` |
| 配布サーバーで起動前に設定を検証する | `sudo -u certpouch certpouch server --check-config --config /etc/certpouch/server-init/server.yaml` |
| 配布サーバーで公開証明書の期限を見る | `sudo -u certpouch certpouch cert status --config /etc/certpouch/server-init/server.yaml` |
| DNS管理ホストでゾーンをプレビューする | `certpouch dns sync --dry-run --config /etc/certpouch/server.yaml` |

証明書の登録には手順3の `cert publish` を使います。NSDへの適用は、[サーバー設定例](../configs/server.example.yaml)をもとに候補を生成し、[検証・差分確認・適用の手順](dns-sync.md)へ進みます。

## 通らなかったとき

| 症状 | 最初に確認すること |
| --- | --- |
| healthに接続できない | 配布サーバーの待ち受けIPとポート、経路、サーバー証明書のSAN、クライアントのTLS用CA |
| PullがHTTP 401 | クライアントID、トークンとそのハッシュ、クライアントの有効状態。台帳の期限を設定した場合は取得記録も確認 |
| 設定した証明書名が一覧にない | サーバーへの登録名、台帳で同名レコードに解決されるIP、配布サーバーから見た実際の接続元IP |
| 台帳ファイルの権限で拒否される | 実行環境で見えるファイル権限。WSLからWindows側の共有パスを読むと広い権限に見える場合は、Linux側へコピーし、利用ユーザーにだけ必要な権限を与える |
| サービス反映に失敗した | `status`、ユニットのジャーナル、配置先の通過権限、秘密鍵の読取権限。[手動復旧](client-deployment.md) |

Sheetsからの直接取得、NSD管理、CA自動更新は必要になった時点で設定します。[Sheets取得](sheets-fetch.md)・[NSD管理](dns-sync.md)・[CA更新](certificate-renewal.md)
