# NSD向けゾーン生成

`certpouch dns sync` はGoogle Sheetsキャッシュまたはシートを個別にエクスポートしたCSVから候補ゾーンを生成する。キャッシュの更新には [`dns fetch`](sheets-fetch.md) を使える。[DNS設定例](../configs/dns.example.yaml)で、台帳のパス、正引きゾーン名、SOA、ネームサーバー、TTLとserialを指定する。CSVの場合は `dns_inventory.cache_file` を `csv_files: ["/path/to/export.csv"]` に置き換える。複数ファイルでは、シートごとに初期プレフィックスをリセットする。

```sh
certpouch dns sync --config /etc/certpouch/dns.yaml --dry-run
certpouch dns sync --config /etc/certpouch/dns.yaml --stage-dir /var/lib/certpouch/dns-staging
```

`--dry-run` はゾーン候補を標準出力に表示する。`--stage-dir` は事前に作成した絶対パスを指定する。新しい候補ディレクトリに個別のゾーンファイルを書き出し、すべての `nsd-checkzone` が成功した場合にディレクトリの場所を表示する。生成コマンドは稼働中のゾーンの置換やNSDの再読み込みを行わない。

既存キャッシュからIPv4のAレコードと、アドレスごとの/24逆引きゾーンのPTRレコードを生成する。同じIPの別名はそれぞれPTR候補になる。生成器はIPv6のAAAAも扱うが、現在のキャッシュ形式はIPv4のみで、IPv6逆引きは未対応。空の台帳、ゾーン外のホスト名、アドレスがないゾーン内ネームサーバーはエラーになる。

serialは現在の稼働ゾーンを確認したうえで設定する。`zones.previous_serials` にゾーン名ごとの現在のSOA serialを指定すると、`zones.serial` で指定した値がそれらより新しいか確認する。`zones.serial` を省略すると、全候補ゾーンに現在のserialを指定することが必要になり、ゾーンごとに1進める。比較と周回は [RFC 1982](https://www.rfc-editor.org/rfc/rfc1982.html) に従う。

初回適用後は、設定から `zones.serial` と `zones.previous_serials` を省略し、`--serial-from` で管理ディレクトリを指定できる。`current` の各ゾーンが持つSOA serialを1進めて候補を生成する。候補と管理中のゾーンの組み合わせが違う場合は生成を拒否する。適用時にはNSDが返すSOAとも照合し、レコードに変更がないゾーンは現在のserialを維持する。

```sh
certpouch dns sync --config /etc/certpouch/dns.yaml \
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

## NSDへの適用

適用コマンドはLinuxの専用管理ディレクトリを使用する。既存のゾーン定義や静的レコードと候補を比較してから、空の管理ディレクトリに最初の候補を登録する。初期登録はNSDに接続しない。

```sh
certpouch dns apply --initialize \
  --candidate /var/lib/certpouch/dns-staging/certpouch-zones-12345 \
  --zone-dir /var/lib/certpouch/nsd-zones
```

NSDの各ゾーン定義を `/var/lib/certpouch/nsd-zones/current/<ゾーン名>.zone` に向け、使用するNSDユーザーに管理ディレクトリの通過権限とファイルの読み取り権限を与える。候補は0640、世代ディレクトリは0750。NSDと別ユーザーで適用する場合は、管理ディレクトリに共有グループとsetgidを設定してグループを継承させる。リモート制御を有効にし、NSDが最初の世代を配信していることを確認する。

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

ゾーンの追加・削除はNSD側で別途設定する。レコードやゾーンの差分を確認してから適用する。
