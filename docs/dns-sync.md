# NSD向けゾーン生成と適用

DNS台帳からNSD向けのゾーンを生成し、検証してから適用する手順です。運用の形に合わせて選んでください。

| 運用 | 使うコマンド | 向いている構成 |
| --- | --- | --- |
| [自動運用](#sheetsからの自動運用) | `pipeline --watch` | Sheetsの変更をDNSと証明書へ定期的に反映する。`certificates.targets` を省略して台帳上のすべての名前を発行する構成では、この方法を使う |
| [手動運用](#手動で反映する) | `pipeline`、`dns diff`、`pipeline --apply` | 差分を確認してから反映したい。名前を `targets` で固定した構成か、証明書を発行しない構成 |
| [個別のコマンド](#候補を生成する) | `dns sync`、`dns diff`、`dns apply` | 初期登録、移行前の比較、細かな制御 |

## Sheetsからの自動運用

NSDへの初期登録が済んだ環境では、次のコマンドで取得・反映・発行を定期実行できます。

```sh
certpouch pipeline --watch --config /etc/certpouch/server.yaml
```

1. Sheetsから台帳を取得します。間隔は `inventory.poll_interval`（既定 `3m`）で、失敗時は待ち時間を延ばします。
2. NSDに反映し、SOAを確認してから台帳キャッシュを更新します。
3. 証明書の発行・更新を実行します。発行が失敗しても、配信中のDNSは維持されます。

### 起動時のゾーン準備

[設定例](../configs/server.example.yaml)を使います。

1. NSDの設定に `include: "/etc/nsd/certpouch-zones.conf"` を追加します。
2. 先頭行が `# Managed by CertPouch` の専用ファイルを作ります。管理対象ゾーンの定義は、このファイルに集約されます。
3. `dns.zones.reverse_zones` に、使用するIPv4 /24逆引きゾーンを列挙します。

- 起動時に専用ファイルと管理世代を切り替え、`nsd-checkconf`、`nsd-control reconfig`、SOAの照合を実行します。
- レンジを変える場合は、このリストを変更して同期プロセスを再起動します。
- すでにNSDで運用中のゾーンを移す場合は、現行のSOAとレコードを確認してから管理世代を初期登録します。
- 切り戻しにも失敗した場合は、管理ディレクトリの `.dns-apply-lock/startup-recovery` を確認します。

### 常駐させる

[pipeline用systemd unit](../configs/systemd/certpouch-pipeline.service)を使えます。

- 同じ設定に対して、専用の取得タイマーや更新タイマーを併用しないでください。
- 同期プロセスと配布サーバーを別ユーザーで動かす場合は、同期ユーザーが所有し、配布グループが読める0640のキャッシュを事前に用意します。
- 配布サーバーは、初回の同期が成功してから起動します。
- 配布サーバーのユーザーには、TLS鍵・トークンハッシュ・公開ストアを読める権限を付けます。署名CA秘密鍵を読めるのは同期ユーザーだけにします。

### 台帳の期限

[設定例](../configs/server.example.yaml)では `inventory.max_age: "30m"` を指定しています。期限を超えると、配布APIのhealthは503を返し、新規の取得を拒否します。最後にDNS反映を確認できた台帳の取得時刻は、`last_successful_fetch` で確認できます。

```sh
certpouch dns fetch --status --config /etc/certpouch/server.yaml
```

## 手動で反映する

この流れは内部で `dns fetch` を使うため、`certificates.targets` を省略して台帳上のすべての名前を発行する構成では使えません。その構成では `pipeline --once` または `--watch` を使います。

```sh
certpouch pipeline --config /etc/certpouch/server.yaml
certpouch dns diff --zone-dir /var/lib/certpouch/nsd-zones --candidate /absolute/path/to/candidate
certpouch pipeline --config /etc/certpouch/server.yaml --apply /absolute/path/to/candidate
certpouch cert renew --config /etc/certpouch/server.yaml
```

1. `pipeline` がSheetsを読み取り、検証済みの候補のパスを出力します。
2. `dns diff` で差分を確認します。
3. そのパスを明示して `pipeline --apply` で反映します。
4. 証明書の発行・公開は、反映の成功後に個別に実行します。

## 候補を生成する

`certpouch dns sync` は、Google Sheetsのキャッシュ、またはシートを個別にエクスポートしたCSVから候補ゾーンを生成します。

- キャッシュの更新には [`dns fetch`](sheets-fetch.md) を使えます。
- [サーバー設定例](../configs/server.example.yaml)で、台帳のパス、正引きゾーン名、SOA、ネームサーバー、TTLを指定します。
- CSVの場合は、`inventory.cache_file` を `inventory.csv_files: ["/path/to/export.csv"]` に置き換えます。複数のファイルでは、シートごとに初期プレフィックスをリセットします。

```sh
certpouch dns sync --config /etc/certpouch/server.yaml --dry-run
certpouch dns sync --config /etc/certpouch/server.yaml --serial 2026112801 \
  --stage-dir /var/lib/certpouch/dns-staging
```

| オプション | 動作 |
| --- | --- |
| `--dry-run` | ゾーン候補を標準出力に表示します |
| `--stage-dir` | 事前に作成した絶対パスを指定します。新しい候補ディレクトリに個別のゾーンファイルを書き出し、すべての `nsd-checkzone` が成功するとディレクトリの場所を表示します |

生成コマンドは、稼働中のゾーンの置換やNSDの再読み込みを行いません。

### 生成されるレコード

- 台帳のIPv4からAレコードと、アドレスごとの/24逆引きゾーンのPTRレコードを生成します。同じIPの別名も、それぞれPTRの候補になります。
- 生成器はIPv6のAAAAも扱いますが、現在のキャッシュ形式はIPv4のみで、IPv6逆引きは未対応です。
- `dns.zones.reverse_zones` に管理するIPv4逆引きゾーン名（例 `reverse_zones: ["2.0.192.in-addr.arpa"]`）を指定すると、PTRが0件でもそのゾーンを候補に残します。指定外のIPv4逆引きゾーンが必要な台帳は、候補の生成に失敗します。
- 空の台帳、ゾーン外のホスト名、アドレスのないゾーン内ネームサーバーはエラーになります。

### serialの指定

serialの出所は1つだけ選びます。比較と周回は [RFC 1982](https://www.rfc-editor.org/rfc/rfc1982.html) に従います。

| 指定 | 使う場面 | 動作 |
| --- | --- | --- |
| `--serial N` | 初回の候補 | 全候補ゾーンに同じ値を使います。`N` は、現在NSDが配信しているserialより新しい値にします（例は日付形式） |
| `--serial-from DIR` | 初回の適用後 | 管理ディレクトリの `current` にある各ゾーンのserialを1進めます |
| `--serial-from-baseline PATH` | NSD登録前に、ステージ済みの候補を基準に作り直す | 基準の各ゾーンのserialを1進めます |
| 設定の `dns.zones.previous_serials` | 管理世代を持つ前の個別実行 | ゾーンごとの現在のSOA serialを書くと、`dns.zones.serial` がそれより新しいか確認します。`dns.zones.serial` を省略すると、ゾーンごとに1進めます |
| なし | `--dry-run` のみ | serial 1で表示します |

初期化後は `--serial-from` または `pipeline` が `dns.nsd.zone_dir` の現行世代からserialを進めるため、設定ファイルのserialは不要です。

```sh
certpouch dns sync --config /etc/certpouch/server.yaml \
  --serial-from /var/lib/certpouch/nsd-zones \
  --stage-dir /var/lib/certpouch/dns-staging
```

- 候補と管理中のゾーンの組み合わせが違う場合、生成を拒否します。
- 適用時には、NSDが返すSOAとも照合します。レコードに変更がないゾーンは、現在のserialを維持します。
- `--serial-from-baseline` は、`--serial` や設定の `dns.zones.serial`・`dns.zones.previous_serials` と併用できません。基準にないゾーンを追加するときは、代わりに `--serial N` を使います。

## 固定レコードを加える

`dns.zones.static_records` に、台帳にない固定レコードを指定できます。次は、正引きゾーンに別名とTXT、生成済みの逆引きゾーンに固定PTRを追加する例です。

```yaml
static_records:
  - {owner: "www.example.internal.", type: "CNAME", value: "app.example.internal."}
  - {owner: "_policy.example.internal.", type: "TXT", value: "status=active"}
  - {zone: "2.0.192.in-addr.arpa", owner: "53.2.0.192.in-addr.arpa.", type: "PTR", value: "ns.example.internal."}
```

| 項目 | 規則 |
| --- | --- |
| `zone` | 省略すると正引きゾーン |
| `owner` | 完全修飾名、またはゾーン頂点を表す `@` |
| `type` | A、AAAA、CNAME、MX、TXT、PTR。MXでは `priority` が必須 |
| TXTの値 | 引用符・バックスラッシュ・ドル記号を含まない、255バイト以内の表示可能なASCII |

- 固定のAからPTRを生成する場合は、PTRも明示します。
- CNAMEと他のレコードの衝突、複数の異なる参照先、循環はエラーになります。
- 既存のゾーンファイルをそのまま取り込む機能はありません。移行前に必要な固定レコードを設定へ移し、既存ゾーンと生成結果を比較してください。

## 候補を比較する

Linuxでは、CertPouch形式で保存した候補があれば、NSDへの初期登録前でも比較できます。基準と候補には、`dns sync --stage-dir` が出力したディレクトリの絶対パスを指定します。追加・削除されたゾーンとレコードをJSONで確認できます。

```sh
certpouch dns diff --baseline /var/lib/certpouch/staging/old \
  --candidate /var/lib/certpouch/staging/new
```

## NSDへの適用

適用コマンドは、Linuxの専用管理ディレクトリを使います。ゾーンの追加・削除はNSD側で別途設定してください。

### 初期登録

1. 既存のゾーン定義や静的レコードと候補を比較します。
2. 初回の候補を `dns sync --stage-dir DIR --serial N` で作ります。
3. 空の管理ディレクトリに、最初の候補を登録します。初期登録はNSDに接続しません。

   ```sh
   certpouch dns apply --initialize \
     --candidate /var/lib/certpouch/dns-staging/certpouch-zones-12345 \
     --zone-dir /var/lib/certpouch/nsd-zones
   ```

4. NSDの各ゾーン定義を `/var/lib/certpouch/nsd-zones/current/<ゾーン名>.zone` に向けます。
5. NSDのユーザーに、管理ディレクトリの通過権限とファイルの読み取り権限を与えます。適用後の世代では、ゾーンファイルが0640、世代ディレクトリが0750になります。NSDと別のユーザーで適用する場合は、管理ディレクトリに共有グループとsetgidを設定し、グループを継承させます。
6. リモート制御を有効にし、NSDが最初の世代を配信していることを確認します。

### 更新

1. 適用前に、現在の世代と候補を比較します。ゾーンごとに追加・削除された行と新旧のserialをJSONで表示します。serialだけの変更は `serial_only` と表示されます。比較はファイルやNSDの状態を変更しません。

   ```sh
   certpouch dns diff --candidate PATH --zone-dir PATH
   ```

2. 適用します。

   ```sh
   certpouch dns apply \
     --candidate /var/lib/certpouch/dns-staging/certpouch-zones-67890 \
     --zone-dir /var/lib/certpouch/nsd-zones \
     --nsd-config /etc/nsd/nsd.conf \
     --dns-address 127.0.0.1:53
   ```

適用時の動作:

- 全候補を再検証し、現在NSDが配信しているserialと管理ディレクトリの内容を照合します。
- 変更があるゾーンだけにreloadを要求します。
- ローカルのNSDへ直接TCPでSOAを問い合わせ、期待したserialが返った時点で成功とします。
- `--dns-address` には、リゾルバーを介さない、対象NSDのループバックアドレスとポートを指定します。[NSDの制御コマンド](https://nsd.docs.nlnetlabs.nl/en/latest/manpages/nsd-control.html)

### 削除件数の上限

`dns apply --max-removed-records N` を指定すると、既存ゾーン全体で削除・置換されるレコード行が `N` 件を超える候補を拒否します。

- `0` を指定すると、レコードの削除・置換がある候補をすべて拒否します。
- 同じ名前の参照先を変える更新も、旧レコード1件の置換として数えます。
- SOAとゾーン共通TTLの変更は件数に含めません。
- 判定は、適用ロックの取得後、NSDへの問い合わせや候補の適用より前に行います。
- 既定値の `-1` は件数制限を無効にします。

### 失敗と復旧

- 反映に失敗した場合は、旧レコードを候補より新しいserialで再適用します。
- ファイルの参照先は一度に切り替わりますが、NSDはゾーンごとに反映します。
- 異常終了や切り戻しの失敗で `.dns-apply-lock/recovery` が残った場合は、次回の処理で、記録した世代とNSDのSOA serialを照合して復旧します。
- 起動時のゾーン登録が中断した場合は、専用includeを使う同期が `.dns-apply-lock/startup-recovery` を読み、旧ゾーン構成を再適用します。
- 記録にない世代やserialが見つかった場合は、記録を保持して停止します。

### 旧版からの切り替え

- 切り替える前に、旧版の同期・DNS適用を停止します。
- 新しいバイナリの排他は、プロセスの終了時に解放されます。
- 稼働中は `.dns-apply-guard` を取り除かないでください。
