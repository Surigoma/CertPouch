# 発行済み証明書を手動で配布する

既存CAで発行済みの証明書を、既存のDNS台帳を使ってLinuxホスト1台へ配布する手順です。

- 配布サーバーと取得ホストは別のマシンとし、サーバーには `server.yaml`、取得ホストには `client.yaml` を置きます。[設定コマンド](configure.md)
- Sheetsから自動運用を始める場合は[QuickStart](quickstart.md)を参照してください。
- Linuxへのバイナリ配置は[インストール手順](linux-installation.md)を参照してください。
- 以下のドメイン、IP、サービス名、パスはすべて例です。

## 1. 用意するもの

| 用意するもの | 入手先・置き場所 |
| --- | --- |
| CertPouchのLinuxバイナリ | 両ホストの `/usr/local/bin/certpouch` |
| 配布サーバー用のTLS証明書と秘密鍵 | 配布サーバーに置く。SANは取得ホストが接続する名前（例 `cert-server.example.internal`）に合わせる。取得ホストには、この証明書を信頼するCAを置く |
| 配布するリーフ証明書、秘密鍵、必要な中間証明書 | 既存の発行手順から取得し、配布サーバーの管理者だけが読める場所に置く |
| 配布する証明書の発行者証明書と信頼CA | 発行者は、リーフに直接署名したCA。両ホストに置く |
| 既存のDNS台帳 | Google Sheetsの取得結果を保存したJSONキャッシュ、またはCSVを配布サーバーに置く。証明書の論理名と一致するレコードがあることを確認する |
| 取得ホストの利用サービス | 証明書を使うsystemdユニット（例 `nginx.service`） |

配布サーバー自身のTLS用CAと、配布する証明書を検証するCAは用途が別です。既存構成で同じCAを使っている場合は、同じバンドルを参照できます。

## 2. `configure` で初期設定を作る

`configure` を使うと、手順3のトークン作成と、手順4・5のYAML作成を省略できます。手作業で設定する場合は手順3へ進んでください。

事前にCAファイル、サーバーのHTTPS証明書と秘密鍵、台帳を用意します。`--out` には新規ディレクトリを指定し、親ディレクトリは先に作成しておきます。

1. 取得ホストで、トークンとクライアント設定を作ります。

   ```sh
   sudo certpouch configure client --out /etc/certpouch/client-init \
     --id web-server-01 --name web.example.internal \
     --url https://cert-server.example.internal:8443 \
     --server-ca /etc/ssl/certpouch/server-ca.crt \
     --trust-ca /etc/ssl/certpouch/issuers.pem \
     --issuer /etc/ssl/certpouch/intermediate.crt --unit nginx.service
   ```

2. 生成された `client.token.sha256` だけを、配布サーバーの管理者へ安全に渡します。
3. 配布サーバーで、受け取ったハッシュと台帳を指定して設定を作ります。JSONの台帳キャッシュを使う場合は、`--csv` の代わりに `--cache` を指定します。

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

生成結果:

- 公開ストアは `/etc/certpouch/server-init/publications`、配置先は `/etc/certpouch/client-init/deployment` です。
- 以降の例の `/etc/certpouch/server.yaml` と `/etc/certpouch/client.yaml` は、それぞれ `server-init/server.yaml` と `client-init/client.yaml` に読み替えます。systemdユニットの `--config` も同じパスにします。
- DNS SANは証明書名が既定です。異なる場合は両コマンドに `--dns-san` を指定します。
- 生成ディレクトリは0700、設定と資格情報は0600です。既存の出力先は上書きしません。
- サーバーのTLS秘密鍵、CA、台帳はコピーされないため、参照先の権限も確認します。

この後は手順4の[実行ユーザーと権限を用意する](#実行ユーザーと権限を用意する)へ進みます。そこで生成先の権限を整え、systemdユニットを配置します。

## 3. 手動でクライアント資格情報を作る

信頼できる管理端末のBashで、クライアントごとに別のトークンを作ります。トークンは画面に表示されません。

```bash
set -euo pipefail
umask 077
credentials_dir=$(mktemp -d)
openssl rand 32 | openssl base64 -A | tr '/+' '_-' | tr -d '=' > "$credentials_dir/client.token"
sha256sum "$credentials_dir/client.token" | awk '{print $1}' > "$credentials_dir/web-server-01.sha256"
```

| ファイル | 置き場所 | 読めるようにするユーザー |
| --- | --- | --- |
| `client.token` | 取得ホストの `/etc/certpouch/client.token` | Pullの実行ユーザー |
| `web-server-01.sha256` | 配布サーバーの `/etc/certpouch/credentials/web-server-01.sha256` | 配布サーバーの実行ユーザー |

どちらも安全な経路で配置し、0600にします。配布サーバーが持つのはハッシュだけです。

## 4. 配布サーバーを設定して起動する

### server.yaml を作る

配布だけを行うサーバーでは、次の4節の `/etc/certpouch/server.yaml` を作ります。全項目は[サーバー設定例](../configs/server.example.yaml)を参照してください。

```yaml
inventory:
  cache_file: "/var/lib/certpouch/inventory.json"
  fallback_prefix: "192.0.2"
dns:
  zone: "example.internal"
certificates:
  store_dir: "/var/lib/certpouch/certificates"
  trust_ca_file: "/etc/certpouch/issuers.pem"
  issuer:
    certificate_file: "/etc/certpouch/intermediate.crt"
  targets:
    - name: "web.example.internal"
distribution:
  listen: "192.0.2.20:8443"
  tls:
    certificate_file: "/etc/certpouch/tls/server.crt"
    private_key_file: "/etc/certpouch/tls/server.key"
  clients:
    - id: "web-server-01"
      enabled: true
      token_hash_file: "/etc/certpouch/credentials/web-server-01.sha256"
```

| YAMLキー | 設定する値 |
| --- | --- |
| `inventory.cache_file` | 台帳のJSONキャッシュの絶対パス。CSVの場合は代わりに `csv_files: ["/path/to/inventory.csv"]` |
| `inventory.fallback_prefix`、`dns.zone` | 台帳でプレフィックスが指定される前に使うIPv4プレフィックスと、台帳上の名前に補うゾーン名。[台帳の記入例](dns-inventory.md#台帳の記入例) |
| `certificates.store_dir`、`trust_ca_file` | 公開世代の保存先と、配布する証明書を検証するCAバンドル |
| `certificates.issuer.certificate_file` | リーフに直接署名した発行者の証明書。CA秘密鍵は指定しない |
| `certificates.targets[0].name` | 台帳の完全修飾名（例 `web.example.internal`）。SANが名前と異なる場合だけ `expected_dns_sans` を追加 |
| `distribution.listen` | 取得ホストから到達できる、サーバー側のIPとポート |
| `distribution.tls.certificate_file`、`private_key_file` | 配布サーバー自身のHTTPS証明書と秘密鍵の絶対パス |
| `distribution.clients[0].id`、`enabled`、`token_hash_file` | クライアントID（例 `web-server-01`）、`true`、手順3で置いたハッシュの絶対パス |

台帳モードでは、配布サーバーから見た取得ホストの接続元IPを、証明書の論理名に対応する台帳のIPと照合します。別ホストから取得させる場合は、そのホスト名を台帳に登録し、`certificate_access_overrides` に追加します。[例外の設定](dns-inventory.md)

### 実行ユーザーと権限を用意する

どちらの設定方法でも、配布サービスの実行ユーザーを用意します。Debian系で `certpouch` ユーザーが未作成の場合は、次を実行します。

```sh
sudo useradd --system --user-group --no-create-home --shell /usr/sbin/nologin certpouch
```

**手動で設定した場合**は、`certificates.store_dir` に指定したパスに公開ストアを作ります。

```sh
sudo install -d -m 0700 -o certpouch -g certpouch /var/lib/certpouch/certificates
```

**rootで `configure server` を実行した場合**は、生成済みの公開ストアを使い、次のように所有者と権限を設定します。

```sh
sudo chgrp certpouch /etc/certpouch/server-init /etc/certpouch/server-init/server.yaml
sudo chmod 0750 /etc/certpouch/server-init
sudo chmod 0640 /etc/certpouch/server-init/server.yaml
sudo chown certpouch:certpouch /etc/certpouch/server-init/client.token.sha256 \
  /etc/certpouch/server-init/publications
```

設定方法にかかわらず、次を確認します。

- `server.yaml` は `root:certpouch` の0640にします。
- サーバーのTLS秘密鍵とトークンハッシュは、`certpouch` が読める0600にします。
- 台帳は、実行ユーザー以外が書き込める場所に置かないでください。
- ファイルに加えて、親ディレクトリの通過権限も確認します。
- [配布サーバー用ユニット](../configs/systemd/certpouch-server.service)を `/etc/systemd/system/` に配置します。`configure server` を使った場合は、ユニットの `ExecStart` の `--config` を `/etc/certpouch/server-init/server.yaml` に変更します。

### 証明書を登録して起動する

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

登録が成功すると、証明書名と世代IDが表示されます。リーフが信頼済みルートCAから直接発行されている場合は `--chain` を省略します。

## 5. 取得ホストで初回Pullを実行する

`configure client` を使った場合は、生成した設定と配置先を使い、下の手順1から進みます。手動で設定する場合は、[クライアント設定例](../configs/client.example.yaml)を `/etc/certpouch/client.yaml` にコピーして次の値を設定します。

| YAMLキー | 設定する値 |
| --- | --- |
| `server.url`、`server.ca_file` | 配布サーバーのHTTPS URLと、そのTLS証明書を信頼するCA。URLのホスト名はサーバー証明書のSANと一致させる |
| `client.id`、`client.token_file` | サーバーのクライアントIDと同じ値と、手順3で置いたトークンの絶対パス |
| `validation.trust_ca_file` | 配布する証明書を検証するCAバンドル |
| `certificates[0].name`、`expected_dns_sans`、`issuer_certificate` | サーバー側の配布名・SAN・直接発行者と一致する値 |
| `certificates[0].destination` | 配置先の絶対パス。事前に作成し、Pullの実行ユーザーに書き込み権限を与える |
| `certificates[0].service.unit`、`action` | 対象のsystemdユニット名と、`reload` または `restart`。停止中のサービスを初回配置後に起動する構成では `restart` |

1. 本番サービスの参照先を変える前に、取得ホストから到達性と認可を確認します。

   ```sh
   curl --fail --cacert /etc/ssl/certpouch/server-ca.crt \
     https://cert-server.example.internal:8443/api/v1/health
   sudo certpouch pull --dry-run --config /etc/certpouch/client.yaml
   ```

   healthの `{"status":"ok"}` でHTTPS接続を確認できます。dry-runに対象名・`install`・世代IDが出れば、トークン認証と接続元認可も通っています。

2. 既存のサービス設定を退避し、サービスの証明書と秘密鍵の参照先を、配置先の `current/fullchain.pem` と `current/private-key.pem` にします。Pullと別ユーザーで動くサービスには、必要なグループ権限を設定します。[配置先の権限と切り戻し](client-deployment.md)
3. Pullを実行し、状態を確認します。

   ```sh
   sudo certpouch pull --config /etc/certpouch/client.yaml
   sudo certpouch status --config /etc/certpouch/client.yaml
   ```

4. statusで対象が `installed` になり、`last_successful_sync` が表示されることを確認します。サービスにもTLS接続して、配信される証明書を確認します。
5. 手動Pullを確認できたら、[定期実行](client-deployment.md#定期実行)へ進みます。

## 普段使うコマンド

`configure server` と `configure client` を使った場合のパスです。手動で設定した場合は、自分のYAMLのパスに置き換えてください。

| 実行する場所・目的 | コマンド |
| --- | --- |
| 取得ホストで更新予定を見る | `sudo certpouch pull --dry-run --config /etc/certpouch/client-init/client.yaml` |
| 取得ホストで更新を反映する | `sudo certpouch pull --config /etc/certpouch/client-init/client.yaml` |
| 取得ホストで配置状態を確認する | `sudo certpouch status --config /etc/certpouch/client-init/client.yaml` |
| 配布サーバーで起動前に設定を検証する | `sudo -u certpouch certpouch server --check-config --config /etc/certpouch/server-init/server.yaml` |
| 配布サーバーで公開証明書の期限を見る | `sudo -u certpouch certpouch cert status --config /etc/certpouch/server-init/server.yaml` |
| DNS管理ホストでゾーンをプレビューする | `certpouch dns sync --dry-run --config /etc/certpouch/server.yaml` |

証明書の登録には手順4の `cert publish` を使います。NSDへの適用は、[サーバー設定例](../configs/server.example.yaml)をもとに候補を生成し、[検証・差分確認・適用の手順](dns-sync.md)へ進みます。

## 通らなかったとき

| 症状 | 最初に確認すること |
| --- | --- |
| healthに接続できない | 配布サーバーの待ち受けIPとポート、経路、サーバー証明書のSAN、クライアントのTLS用CA |
| PullがHTTP 401 | クライアントID、トークンとそのハッシュ、クライアントの有効状態。台帳の期限を設定した場合は取得記録も |
| 設定した証明書名が一覧にない | サーバーへの登録名、台帳で同名レコードに解決されるIP、配布サーバーから見た実際の接続元IP |
| 台帳ファイルの権限で拒否される | 実行環境で見えるファイル権限。WSLからWindows側の共有パスを読むと広い権限に見えるため、Linux側へコピーし、利用ユーザーにだけ必要な権限を与える |
| サービス反映に失敗した | `status`、ユニットのジャーナル、配置先の通過権限、秘密鍵の読み取り権限。[手動復旧](client-deployment.md#手動復旧) |

Sheetsからの直接取得、NSD管理、CA自動更新は、必要になった時点で設定します。[Sheets取得](sheets-fetch.md)・[NSD管理](dns-sync.md)・[CA更新](certificate-renewal.md)
