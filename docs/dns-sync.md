# NSD向けゾーン生成

## Sheetsからの運用手順

```sh
certpouch pipeline --config /etc/certpouch/server.yaml
certpouch dns diff --zone-dir /var/lib/certpouch/nsd-zones --candidate /absolute/path/to/candidate
certpouch pipeline --config /etc/certpouch/server.yaml --apply /absolute/path/to/candidate
certpouch cert renew --config /etc/certpouch/server.yaml
```

最初のコマンドはSheetsを読み取り、検証済み候補のパスを出力する。差分を確認し、そのパスを明示して反映する。パイプライン経由の反映は既存レコードの削除・置換がある場合に停止する。意図した削除・置換は個別の `dns apply` で上限を指定する。証明書発行・公開は反映成功後に個別実行する。

初回は既存NSDへの適用手順に従って管理スナップショットを初期化する。初回候補には `pipeline.dns.zones.serial` を設定して個別の `dns sync` を使う。初期化後は固定serialを外し、`pipeline.dns.deployment.zone_dir` の現行世代からserialを進める。

`certpouch dns sync` はGoogle Sheetsキャッシュまたはシートを個別にエクスポートしたCSVから候補ゾーンを生成する。キャッシュの更新には [`dns fetch`](sheets-fetch.md) を使える。[サーバー設定例](../configs/server.example.yaml)で、台帳のパス、正引きゾーン名、SOA、ネームサーバー、TTLとserialを指定する。CSVの場合は `dns_inventory.cache_file` を `csv_files: ["/path/to/export.csv"]` に置き換える。複数ファイルでは、シートごとに初期プレフィックスをリセットする。

```sh
certpouch dns sync --config /etc/certpouch/server.yaml --dry-run
certpouch dns sync --config /etc/certpouch/server.yaml --stage-dir /var/lib/certpouch/dns-staging
```

`--dry-run` はゾーン候補を標準出力に表示する。`--stage-dir` は事前に作成した絶対パスを指定する。新しい候補ディレクトリに個別のゾーンファイルを書き出し、すべての `nsd-checkzone` が成功した場合にディレクトリの場所を表示する。生成コマンドは稼働中のゾーンの置換やNSDの再読み込みを行わない。

既存キャッシュからIPv4のAレコードと、アドレスごとの/24逆引きゾーンのPTRレコードを生成する。同じIPの別名はそれぞれPTR候補になる。生成器はIPv6のAAAAも扱うが、現在のキャッシュ形式はIPv4のみで、IPv6逆引きは未対応。空の台帳、ゾーン外のホスト名、アドレスがないゾーン内ネームサーバーはエラーになる。

serialは現在の稼働ゾーンを確認したうえで設定する。`zones.previous_serials` にゾーン名ごとの現在のSOA serialを指定すると、`zones.serial` で指定した値がそれらより新しいか確認する。`zones.serial` を省略すると、全候補ゾーンに現在のserialを指定することが必要になり、ゾーンごとに1進める。比較と周回は [RFC 1982](https://www.rfc-editor.org/rfc/rfc1982.html) に従う。

初回適用後は、設定から `zones.serial` と `zones.previous_serials` を省略し、`--serial-from` で管理ディレクトリを指定できる。`current` の各ゾーンが持つSOA serialを1進めて候補を生成する。候補と管理中のゾーンの組み合わせが違う場合は生成を拒否する。適用時にはNSDが返すSOAとも照合し、レコードに変更がないゾーンは現在のserialを維持する。

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

同じゾーンを生成し直す場合、`dns sync --serial-from-baseline PATH` で基準のSOA serialをゾーンごとに1進める。このオプションを使うときは、設定の `zones.serial` と `zones.previous_serials` を省略する。基準にないゾーンを追加するときは、設定側でserialを指定する。

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

反映失敗時は、旧レコードを候補より新しいserialで再適用する。ファイル参照先は一度に切り替わるが、NSDで全ゾーンの反映が同時に完了する保証はない。切り戻しにも失敗した場合は、`.dns-apply-lock/recovery` に旧世代・候補・切り戻し世代を残して、次回の適用を停止する。実行中の適用がないことを確認し、`current` とNSDが返すSOA serialを照合してから手動で復旧する。

`dns apply --max-removed-records N` を指定すると、既存ゾーン全体で削除・置換されるレコード行が `N` 件を超える候補を拒否する。例えば `0` を指定するとレコードの削除・置換がある候補を拒否する。同じ名前の参照先を変える更新も、旧レコード1件の置換として数える。SOAとゾーン共通TTLの変更は件数に含めない。判定は適用ロック取得後、NSDへの問い合わせや候補の適用より前に行う。既定値の `-1` は件数制限を無効にする。

ゾーンの追加・削除はNSD側で別途設定する。レコードやゾーンの差分を確認してから適用する。
