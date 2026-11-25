# 証明書を1台に配布する

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

## `init` で初期設定を作る

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
