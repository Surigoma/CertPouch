# NSD向けゾーン生成

`certpouch dns sync` は既存のGoogle Sheetsキャッシュから候補ゾーンを生成する。[DNS設定例](../configs/dns.example.yaml)で、台帳のパス、正引きゾーン名、SOA、ネームサーバー、TTLとserialを指定する。

```sh
certpouch dns sync --config /etc/certpouch/dns.yaml --dry-run
certpouch dns sync --config /etc/certpouch/dns.yaml --stage-dir /var/lib/certpouch/dns-staging
```

`--dry-run` はゾーン候補を標準出力に表示する。`--stage-dir` は事前に作成した絶対パスを指定する。新しい候補ディレクトリに個別のゾーンファイルを書き出し、すべての `nsd-checkzone` が成功した場合にディレクトリの場所を表示する。生成コマンドは稼働中のゾーンの置換やNSDの再読み込みを行わない。

既存キャッシュからIPv4のAレコードと、アドレスごとの/24逆引きゾーンのPTRレコードを生成する。同じIPの別名はそれぞれPTR候補になる。生成器はIPv6のAAAAも扱うが、現在のキャッシュ形式はIPv4のみで、IPv6逆引きは未対応。空の台帳、ゾーン外のホスト名、アドレスがないゾーン内ネームサーバーはエラーになる。

serialは現在の稼働ゾーンを確認したうえで設定する。`zones.previous_serials` にゾーン名ごとの現在のSOA serialを指定すると、`zones.serial` で指定した値がそれらより新しいか確認する。`zones.serial` を省略すると、全候補ゾーンに現在のserialを指定することが必要になり、ゾーンごとに1進める。比較と周回は [RFC 1982](https://www.rfc-editor.org/rfc/rfc1982.html) に従う。

既存のゾーン定義や静的レコードと候補を比較し、変更と切り戻しを確認してから適用する。稼働ゾーンからのserial自動取得、静的レコードとの統合、稼働中のNSDへの適用は今後の作業。
