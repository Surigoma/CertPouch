# NSD向けゾーン生成

## Sheetsからの運用手順

初期登録済みのNSD環境では、`certpouch pipeline --watch --config /etc/certpouch/server.yaml` で取得・反映・発行を定期実行できる。`inventory.poll_interval` の既定値は `3m`。失敗時は待ち時間を延ばす。NSDでSOAを確認してから台帳キャッシュを更新し、その後に証明書更新を実行する。発行が失敗した場合も配信中のDNSは維持される。

起動時のゾーン準備には[設定例](../configs/server.example.yaml)を使う。NSDの設定に `include: "/etc/nsd/certpouch-zones.conf"` を追加し、先頭行が `# Managed by CertPouch` の専用ファイルを作る。管理対象ゾーンの定義はこのファイルに集約する。既にNSDで運用中のゾーンを移す場合は、現行のSOAとレコードを確認してから管理世代を初期登録する。

`dns.zones.reverse_zones` に使用するIPv4 /24逆引きゾーンを列挙する。レンジを変える場合はこのリストを変更して同期プロセスを再起動する。起動時に専用ファイルと管理世代を切り替え、`nsd-checkconf`、`nsd-control reconfig`、SOA照合を実行する。切り戻しにも失敗した場合は管理ディレクトリの `.dns-apply-lock/startup-recovery` を確認する。

常駐させる場合は [pipeline用systemd unit](../configs/systemd/certpouch-pipeline.service)を使える。専用の取得タイマーと更新タイマーを同じ設定に対して併用しない。同期プロセスと配布サーバーを別ユーザーで動かす場合は、同期ユーザー所有かつ配布グループ読み取り可能な0640のキャッシュを事前に用意する。初回同期成功後に配布サーバーを起動する。配布サーバーのユーザーにはTLS鍵・トークンハッシュ・公開ストアを読める権限を付け、署名CA秘密鍵への権限は同期ユーザーに限る。

[設定例](../configs/server.example.yaml)では `inventory.max_age: "30m"` を指定している。期限を超えると配布APIのhealthは503を返し、新規取得を拒否する。最後にDNS反映を確認できた台帳の取得時刻は `certpouch dns fetch --status --config /etc/certpouch/server.yaml` の `last_successful_fetch` で確認できる。

```sh
certpouch pipeline --config /etc/certpouch/server.yaml
certpouch dns diff --zone-dir /var/lib/certpouch/nsd-zones --candidate /absolute/path/to/candidate
certpouch pipeline --config /etc/certpouch/server.yaml --apply /absolute/path/to/candidate
certpouch cert renew --config /etc/certpouch/server.yaml
```

最初のコマンドはSheetsを読み取り、検証済み候補のパスを出力する。差分を確認し、そのパスを明示して反映する。証明書発行・公開は反映成功後に個別実行する。この手動の流れは内部で `dns fetch` を使うため、`certificates.targets` を省略して台帳上のすべての名前を発行する構成では使えない。その構成では `pipeline --once` または `--watch` を使う。

`dns.zones.reverse_zones` に管理するIPv4逆引きゾーン名を指定すると、PTRが0件になってもそのゾーンを候補に残す。例は `reverse_zones: ["2.0.192.in-addr.arpa"]`。指定外のIPv4逆引きゾーンが必要な台帳は候補生成に失敗する。NSDへのゾーン登録は初回適用手順に従う。

初回は既存NSDへの適用手順に従って管理スナップショットを初期化する。初回候補は `dns sync --stage-dir DIR --serial N` で作る。`N` は現在NSDが配信しているserialより新しい値にする。初期化後は `--serial-from` または `pipeline` が `dns.nsd.zone_dir` の現行世代からserialを進めるため、設定ファイルのserialは不要。

`certpouch dns sync` はGoogle Sheetsキャッシュまたはシートを個別にエクスポートしたCSVから候補ゾーンを生成する。キャッシュの更新には [`dns fetch`](sheets-fetch.md) を使える。[サーバー設定例](../configs/server.example.yaml)で、台帳のパス、正引きゾーン名、SOA、ネームサーバー、TTLを指定する。CSVの場合は `inventory.cache_file` を `inventory.csv_files: ["/path/to/export.csv"]` に置き換える。複数ファイルでは、シートごとに初期プレフィックスをリセットする。

```sh
certpouch dns sync --config /etc/certpouch/server.yaml --dry-run
certpouch dns sync --config /etc/certpouch/server.yaml --serial 2026112801 \
  --stage-dir /var/lib/certpouch/dns-staging
```

`--dry-run` はゾーン候補を標準出力に表示する。初回の `--stage-dir` には、現在NSDが配信しているserialより新しい値を `--serial` で渡す（例は日付形式）。`--stage-dir` は事前に作成した絶対パスを指定する。新しい候補ディレクトリに個別のゾーンファイルを書き出し、すべての `nsd-checkzone` が成功した場合にディレクトリの場所を表示する。生成コマンドは稼働中のゾーンの置換やNSDの再読み込みを行わない。

既存キャッシュからIPv4のAレコードと、アドレスごとの/24逆引きゾーンのPTRレコードを生成する。同じIPの別名はそれぞれPTR候補になる。生成器はIPv6のAAAAも扱うが、現在のキャッシュ形式はIPv4のみで、IPv6逆引きは未対応。空の台帳、ゾーン外のホスト名、アドレスがないゾーン内ネームサーバーはエラーになる。

serialの出所は1つだけ選ぶ。`--serial N` は全候補ゾーンに同じ値を使う。`--serial-from` は管理中の世代、`--serial-from-baseline` はステージ済み候補のserialをゾーンごとに1進める。設定ファイルの `dns.zones.previous_serials` にゾーン名ごとの現在のSOA serialを書くと、`dns.zones.serial` の値がそれらより新しいか確認し、`dns.zones.serial` を省略した場合はゾーンごとに1進める。どれも指定しない `--dry-run` はserial 1で表示する。比較と周回は [RFC 1982](https://www.rfc-editor.org/rfc/rfc1982.html) に従う。

初回適用後は `--serial` の代わりに `--serial-from` で管理ディレクトリを指定する。`current` の各ゾーンが持つSOA serialを1進めて候補を生成する。候補と管理中のゾーンの組み合わせが違う場合は生成を拒否する。適用時にはNSDが返すSOAとも照合し、レコードに変更がないゾーンは現在のserialを維持する。

```sh
certpouch dns sync --config /etc/certpouch/server.yaml \
  --serial-from /var/lib/certpouch/nsd-zones \
  --stage-dir /var/lib/certpouch/dns-staging
```

## 固定レコードを加える

`zones.static_records` に台帳外の固定レコードを指定できる。次は正引きゾーンに別名とTXT、生成済みの逆引きゾーンに固定PTRを追加する例。

```yaml
static_records:
  - {owner: "www.example.internal.", type: "CNAME", value: "app.example.internal."}
  - {owner: "_policy.example.internal.", type: "TXT", value: "status=active"}
  - {zone: "2.0.192.in-addr.arpa", owner: "53.2.0.192.in-addr.arpa.", type: "PTR", value: "ns.example.internal."}
```

`zone` を省略すると正引きゾーンになる。`owner` は完全修飾名か、ゾーン頂点を表す `@` を指定する。A、AAAA、CNAME、MX、TXT、PTRに対応し、MXの `priority` は必須。TXTは引用符・バックスラッシュ・ドル記号を含まない255バイト以内の表示可能なASCIIとする。固定AからPTRを生成する場合はPTRも明示する。CNAMEと他レコードの衝突、複数の異なる参照先、循環はエラーになる。

既存のゾーンファイルをそのまま取り込む機能はない。移行前に必要な固定レコードを設定へ移し、既存ゾーンと生成結果を比較する。

Linuxでは、CertPouch形式で保存した候補があれば、NSDの初期登録前でも比較できる。基準と候補には `dns sync --stage-dir` が出力したディレクトリの絶対パスを指定する。追加・削除されたゾーンとレコードをJSONで確認できる。

```sh
certpouch dns diff --baseline /var/lib/certpouch/staging/old \
  --candidate /var/lib/certpouch/staging/new
```

同じゾーンを生成し直す場合、`dns sync --serial-from-baseline PATH` で基準のSOA serialをゾーンごとに1進める。このオプションは `--serial` や設定の `dns.zones.serial`・`dns.zones.previous_serials` と併用できない。基準にないゾーンを追加するときは、代わりに `--serial N` を使う。

## NSDへの適用

適用コマンドはLinuxの専用管理ディレクトリを使用する。既存のゾーン定義や静的レコードと候補を比較してから、空の管理ディレクトリに最初の候補を登録する。初期登録はNSDに接続しない。

```sh
certpouch dns apply --initialize \
  --candidate /var/lib/certpouch/dns-staging/certpouch-zones-12345 \
  --zone-dir /var/lib/certpouch/nsd-zones
```

NSDの各ゾーン定義を `/var/lib/certpouch/nsd-zones/current/<ゾーン名>.zone` に向け、使用するNSDユーザーに管理ディレクトリの通過権限とファイルの読み取り権限を与える。適用後の世代ではゾーンファイルが0640、世代ディレクトリが0750になる。NSDと別ユーザーで適用する場合は、管理ディレクトリに共有グループとsetgidを設定してグループを継承させる。リモート制御を有効にし、NSDが最初の世代を配信していることを確認する。

初回登録後は、適用前に `dns diff --candidate PATH --zone-dir PATH` で現在の世代と候補を比較できる。ゾーンごとに追加・削除された行と新旧serialをJSONで表示する。serialだけの変更は `serial_only` として表示し、差分確認でファイルやNSDの状態は変更しない。

```sh
certpouch dns apply \
  --candidate /var/lib/certpouch/dns-staging/certpouch-zones-67890 \
  --zone-dir /var/lib/certpouch/nsd-zones \
  --nsd-config /etc/nsd/nsd.conf \
  --dns-address 127.0.0.1:53
```

更新では全候補を再検証し、現在NSDが配信中のserialと管理ディレクトリの内容を照合する。変更があるゾーンだけに対してreloadを要求し、ローカルのNSDへ直接TCPでSOAを問い合わせて期待したserialが返った時点で成功とする。`--dns-address` にはリゾルバーを介さず、対象NSDのループバックアドレスとポートを指定する。[NSDの制御コマンド](https://nsd.docs.nlnetlabs.nl/en/latest/manpages/nsd-control.html)

反映失敗時は、旧レコードを候補より新しいserialで再適用する。ファイル参照先は一度に切り替わる。NSDはゾーンごとに反映する。異常終了や切り戻し失敗で `.dns-apply-lock/recovery` が残った場合は、次回の処理で記録した世代とNSDのSOA serialを照合して復旧する。起動時のゾーン登録の中断からは、専用includeを使う同期で `.dns-apply-lock/startup-recovery` を読み、旧ゾーン構成を再適用する。記録外の世代やserialが見つかった場合は、記録を保持して停止する。

旧版から切り替える前に旧版の同期・DNS適用を停止する。新バイナリの排他はプロセス終了時に解放される。稼働中は `.dns-apply-guard` を取り除かない。

`dns apply --max-removed-records N` を指定すると、既存ゾーン全体で削除・置換されるレコード行が `N` 件を超える候補を拒否する。例えば `0` を指定するとレコードの削除・置換がある候補を拒否する。同じ名前の参照先を変える更新も、旧レコード1件の置換として数える。SOAとゾーン共通TTLの変更は件数に含めない。判定は適用ロック取得後、NSDへの問い合わせや候補の適用より前に行う。既定値の `-1` は件数制限を無効にする。

ゾーンの追加・削除はNSD側で別途設定する。レコードやゾーンの差分を確認してから適用する。
