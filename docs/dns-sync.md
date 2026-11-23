# NSD向けゾーン生成

`certpouch dns sync` は既存のGoogle Sheetsキャッシュまたはシートを個別にエクスポートしたCSVから候補ゾーンを生成する。[DNS設定例](../configs/dns.example.yaml)で、台帳のパス、正引きゾーン名、SOA、ネームサーバー、TTLとserialを指定する。CSVの場合は `dns_inventory.cache_file` を `csv_files: ["/path/to/export.csv"]` に置き換える。複数ファイルでは、シートごとに初期プレフィックスをリセットする。

```sh
certpouch dns sync --config /etc/certpouch/dns.yaml --dry-run
certpouch dns sync --config /etc/certpouch/dns.yaml --stage-dir /var/lib/certpouch/dns-staging
```

`--dry-run` はゾーン候補を標準出力に表示する。`--stage-dir` は事前に作成した絶対パスを指定する。新しい候補ディレクトリに個別のゾーンファイルを書き出し、すべての `nsd-checkzone` が成功した場合にディレクトリの場所を表示する。生成コマンドは稼働中のゾーンの置換やNSDの再読み込みを行わない。

既存キャッシュからIPv4のAレコードと、アドレスごとの/24逆引きゾーンのPTRレコードを生成する。同じIPの別名はそれぞれPTR候補になる。生成器はIPv6のAAAAも扱うが、現在のキャッシュ形式はIPv4のみで、IPv6逆引きは未対応。空の台帳、ゾーン外のホスト名、アドレスがないゾーン内ネームサーバーはエラーになる。

serialは現在の稼働ゾーンを確認したうえで設定する。`zones.previous_serials` にゾーン名ごとの現在のSOA serialを指定すると、`zones.serial` で指定した値がそれらより新しいか確認する。`zones.serial` を省略すると、全候補ゾーンに現在のserialを指定することが必要になり、ゾーンごとに1進める。比較と周回は [RFC 1982](https://www.rfc-editor.org/rfc/rfc1982.html) に従う。

## NSDへの適用

適用コマンドはLinuxの専用管理ディレクトリを使用する。既存のゾーン定義や静的レコードと候補を比較してから、空の管理ディレクトリに最初の候補を登録する。初期登録はNSDに接続しない。

```sh
certpouch dns apply --initialize \
  --candidate /var/lib/certpouch/dns-staging/certpouch-zones-12345 \
  --zone-dir /var/lib/certpouch/nsd-zones
```

NSDの各ゾーン定義を `/var/lib/certpouch/nsd-zones/current/<ゾーン名>.zone` に向け、使用するNSDユーザーに管理ディレクトリの通過権限とファイルの読み取り権限を与える。候補は0640、世代ディレクトリは0750。NSDと別ユーザーで適用する場合は、管理ディレクトリに共有グループとsetgidを設定してグループを継承させる。リモート制御を有効にし、NSDが最初の世代を配信していることを確認する。

```sh
certpouch dns apply \
  --candidate /var/lib/certpouch/dns-staging/certpouch-zones-67890 \
  --zone-dir /var/lib/certpouch/nsd-zones \
  --nsd-config /etc/nsd/nsd.conf \
  --dns-address 127.0.0.1:53
```

更新では全候補を再検証し、現在NSDが配信中のserialと管理ディレクトリの内容を照合する。変更があるゾーンだけに対してreloadを要求し、ローカルのNSDへ直接TCPでSOAを問い合わせて期待したserialが返った時点で成功とする。`--dns-address` にはリゾルバーを介さず、対象NSDのループバックアドレスとポートを指定する。[NSDの制御コマンド](https://nsd.docs.nlnetlabs.nl/en/latest/manpages/nsd-control.html)

反映失敗時は、旧レコードを候補より新しいserialで再適用する。ファイル参照先は一度に切り替わるが、NSDで全ゾーンの反映が同時に完了する保証はない。切り戻しにも失敗した場合は、`.dns-apply-lock/recovery` に旧世代・候補・切り戻し世代を残して、次回の適用を停止する。実行中の適用がないことを確認し、`current` とNSDが返すSOA serialを照合してから手動で復旧する。

ゾーンの追加・削除、既存の静的レコードとの統合、稼働ゾーンからのserial自動取得は今後の作業。レコードやゾーンの差分を確認してから適用する。
