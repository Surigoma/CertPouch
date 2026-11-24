# Changelog

## [0.2.1](https://github.com/Surigoma/CertPouch/compare/v0.2.0...v0.2.1) (2026-11-24)


### Bug Fixes

* **deps:** remediate OpenTelemetry vulnerability ([bba278d](https://github.com/Surigoma/CertPouch/commit/bba278d370b66d9626aa9661b9c49df5b14c7d5c))

## [0.2.0](https://github.com/Surigoma/CertPouch/compare/v0.1.0...v0.2.0) (2026-11-24)


### Features

* add Linux setup and guided configuration ([70ed3f2](https://github.com/Surigoma/CertPouch/commit/70ed3f207c39f7957d5e2d745e76bce14237e5d3))

## 0.1.0 (2026-11-23)


### Features

* add authenticated HTTPS certificate distribution ([d5cc743](https://github.com/Surigoma/CertPouch/commit/d5cc74350cbff422441fd23059ad3546a2c1494f))
* add systemd service for certificate distribution ([40706e5](https://github.com/Surigoma/CertPouch/commit/40706e5a854104aab1853bab0f8f29c9031ec405))
* apply managed NSD snapshots and read sheet exports ([cd11cef](https://github.com/Surigoma/CertPouch/commit/cd11cefa9226ea260085cfb17257f06ceff55fd6))
* authorize certificate downloads from DNS inventory ([18a6b26](https://github.com/Surigoma/CertPouch/commit/18a6b26a8d1dc3e5715ce41c9e61e9bba7bf5eca))
* bootstrap distribution configuration with init ([83d1de2](https://github.com/Surigoma/CertPouch/commit/83d1de222a84669b678806f521f25584fa98d605))
* compare staged DNS snapshots before initialization ([12c4852](https://github.com/Surigoma/CertPouch/commit/12c4852ad9497f5ee83cf939bdd3c50aa2f2f25c))
* configure deployment TLS verification timeout ([0b9997f](https://github.com/Surigoma/CertPouch/commit/0b9997f7c2a44cd7109a839647ee9d079de76bf2))
* deploy certificate generations through pull ([5d1c691](https://github.com/Surigoma/CertPouch/commit/5d1c691fa4557139b7ce7e47e2c69dbb6a4ba945))
* expire stale DNS inventory authorization ([bff7cff](https://github.com/Surigoma/CertPouch/commit/bff7cffffd400c9931cf1466fdeb41b32065e090))
* fetch DNS inventory with the Google Sheets client ([5763839](https://github.com/Surigoma/CertPouch/commit/5763839fd2b75e9ec7daac668ecee72f0475f0a4))
* generate validated NSD zone candidates ([f8ee809](https://github.com/Surigoma/CertPouch/commit/f8ee809c751ac8f052bd89f7bce4a78bcbaec040))
* limit DNS record removals during activation ([37d69ed](https://github.com/Surigoma/CertPouch/commit/37d69edf762e3d3920608cf44309d7cf0fcae5c9))
* log certificate distribution outcomes ([9285232](https://github.com/Surigoma/CertPouch/commit/9285232d232dd7584c7e89db598e03d93a05ebb0))
* merge static DNS records and preview zone changes ([b59b748](https://github.com/Surigoma/CertPouch/commit/b59b7486d6d88433cee33f036303e849d18fa0fb))
* monitor expiry of published certificates ([7657930](https://github.com/Surigoma/CertPouch/commit/7657930f24724058c8b03c815a25e0faa7111a9f))
* preview certificate updates from the pull client ([aa16b18](https://github.com/Surigoma/CertPouch/commit/aa16b18b4edb3870a89c0c82cd53780f0038b8e5))
* publish validated certificate generations ([665803c](https://github.com/Surigoma/CertPouch/commit/665803cd00150d7515551d8420b2bc22fec42522))
* recover interrupted certificate deployments ([5c3c65a](https://github.com/Surigoma/CertPouch/commit/5c3c65a6524956506df7bd792da8ccfd69858104))
* refresh certificate publications without restarting ([16585fc](https://github.com/Surigoma/CertPouch/commit/16585fcf0d224e708cbd763c5f84eb5ecfc98cc0))
* refresh DNS inventory authorization without restarting ([3089131](https://github.com/Surigoma/CertPouch/commit/308913116dcb0e6de85f673a87cad299af75ea07))
* reload client revocations without restarting ([7e2de98](https://github.com/Surigoma/CertPouch/commit/7e2de9850705014c9a10412e7cba31b922f3e874))
* renew certificates with an existing private CA ([fdc054f](https://github.com/Surigoma/CertPouch/commit/fdc054f276f3c893751793c17fa84b15b6e9e0dc))
* report certificate status and last successful sync ([19ddc04](https://github.com/Surigoma/CertPouch/commit/19ddc045da293a556158a64d40a0d369d37137be))
* require client certificates for optional mutual TLS ([b71ccac](https://github.com/Surigoma/CertPouch/commit/b71ccacd6ad32c488ad7df4b45a628e891daf96d))
* support client deployment commands ([20ed6bc](https://github.com/Surigoma/CertPouch/commit/20ed6bcecd6f46d7f27dc5bc371948d69f521697))
* validate server configuration before listening ([8d87391](https://github.com/Surigoma/CertPouch/commit/8d8739104b11b46c0df2695a56427fe520e5dfee))
* verify the serving certificate after deployment ([0540156](https://github.com/Surigoma/CertPouch/commit/05401560ea54aad21ee2e4ed22c69c7116097d98))


### Bug Fixes

* detect damaged deployments and retain pull failures ([009ab2d](https://github.com/Surigoma/CertPouch/commit/009ab2d0ef11265b00ac5bd519f1e12fc2a8df7f))
* preserve certificate directory access during scheduled pulls ([70f3d1b](https://github.com/Surigoma/CertPouch/commit/70f3d1b19672b3757c2b0499c807486160c61e06))
* preserve the TLS verification failure on timeout ([f8d3f89](https://github.com/Surigoma/CertPouch/commit/f8d3f89e574218e81060df8dc1be6e2a89086fda))
* report publication store failures in health checks ([7254739](https://github.com/Surigoma/CertPouch/commit/7254739649c37d5034a3b40d7b3dd6c2b35653a6))
* stage release archives before writing output ([0781ad4](https://github.com/Surigoma/CertPouch/commit/0781ad4a1b4239efbf11ec3bb91aa023aeeb363a))
* update Go and vulnerable networking dependencies ([97da404](https://github.com/Surigoma/CertPouch/commit/97da4048943bf75d1e0367ee2452a0cc3639f32c))
* verify release tag before attaching artifacts ([a45bb2d](https://github.com/Surigoma/CertPouch/commit/a45bb2d9323bc68c2a6eec6e07589351a15b3056))
