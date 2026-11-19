# Changelog

## [0.4.1](https://github.com/RigelBuild/meissa/compare/go/v0.4.0...go/v0.4.1) (2026-11-19)


### Bug Fixes

* **codeshape:** cgo-skip wording, host-cgo test, caller lint cache root (RIG-11035) ([#95](https://github.com/RigelBuild/meissa/issues/95)) ([eb29357](https://github.com/RigelBuild/meissa/commit/eb29357f4b24031786b92a66738405e4bfe7bb55))
* **codeshape:** match BiomeVersion to the CI biome (RIG-10601) ([#91](https://github.com/RigelBuild/meissa/issues/91)) ([99c878c](https://github.com/RigelBuild/meissa/commit/99c878c5bc2b72a6f1583a5104a16f5e8489c99a))
* **codeshape:** measure Go files built only for other GOOS (RIG-10887) ([#94](https://github.com/RigelBuild/meissa/issues/94)) ([703fceb](https://github.com/RigelBuild/meissa/commit/703fceb900f8440eb57e165f7f40e824978c8511))
* **codeshape:** report unparseable TypeScript as unmeasured (RIG-11131) ([#96](https://github.com/RigelBuild/meissa/issues/96)) ([05a41fb](https://github.com/RigelBuild/meissa/commit/05a41fbb7d1ef9d38dcbb436737a9a5fbc44f5c9))
* **deps:** update module github.com/pelletier/go-toml/v2 to v2.4.3 ([#67](https://github.com/RigelBuild/meissa/issues/67)) ([75d1b61](https://github.com/RigelBuild/meissa/commit/75d1b617aad8cec46731eddcd2dd65308d44123e))
* **design-ledger-gate:** fall back to git diff when CI_PIPELINE_FILES is unset (RIG-10581) ([#89](https://github.com/RigelBuild/meissa/issues/89)) ([53ca444](https://github.com/RigelBuild/meissa/commit/53ca444fe52e4c8ebfd3ddf5791906e6cfbc2551))
* **design-ledger-gate:** satisfy noinlineerr and noctx in cli tests (RIG-10673) ([#93](https://github.com/RigelBuild/meissa/issues/93)) ([da284ce](https://github.com/RigelBuild/meissa/commit/da284ce84a7b5a1b87b75d166fe79b70b545080c))

## [0.4.0](https://github.com/RigelBuild/meissa/compare/go/v0.3.0...go/v0.4.0) (2026-11-15)


### Features

* **ledgergate:** nested decision areas in the ledger gate (RIG-8979) ([#84](https://github.com/RigelBuild/meissa/issues/84)) ([123d29a](https://github.com/RigelBuild/meissa/commit/123d29a8a1b13263f7562180ca6ade09922e5a2c))
* **meissa:** port the design-ledger tools to Go (RIG-8124) ([#74](https://github.com/RigelBuild/meissa/issues/74)) ([aacea5c](https://github.com/RigelBuild/meissa/commit/aacea5c2a8755fb53cfbf759e51ac6d34c34e75d))

## [0.3.0](https://github.com/RigelBuild/meissa/compare/go/v0.2.0...go/v0.3.0) (2026-11-13)


### Features

* **go:** Go and TypeScript function metrics in code-shape-gate (RIG-8435) ([#69](https://github.com/RigelBuild/meissa/issues/69)) ([90cac06](https://github.com/RigelBuild/meissa/commit/90cac0613420eb338da58982ff23e3476d2cca38))
* **go:** port length-gate to Go (RIG-8128, RIG-8129, RIG-8130) ([#81](https://github.com/RigelBuild/meissa/issues/81)) ([de71450](https://github.com/RigelBuild/meissa/commit/de714507a453c41814ae9dec4d0f1c1fcbf26245))


### Bug Fixes

* **refgate:** encode invalid UTF-8 as a raw U+FFFD on every json encoder (RIG-9660) ([#80](https://github.com/RigelBuild/meissa/issues/80)) ([1ac6dca](https://github.com/RigelBuild/meissa/commit/1ac6dca01cf4cccfd520c5eb055649ad45b9619d))
* **renovate:** classify interrupts by cause and keep restore errors (RIG-8122) ([#66](https://github.com/RigelBuild/meissa/issues/66)) ([a6a4e36](https://github.com/RigelBuild/meissa/commit/a6a4e36855d10f42894b9b5389bf9898ee9d0f00))

## [0.2.0](https://github.com/RigelBuild/meissa/compare/go/v0.1.0...go/v0.2.0) (2026-11-10)


### Features

* **go:** add code-shape-gate with file, directory and ratchet checks (RIG-8435) ([#58](https://github.com/RigelBuild/meissa/issues/58)) ([a96be0d](https://github.com/RigelBuild/meissa/commit/a96be0de9a0ff9ef9c4cf436f9eb25caa439a6a7))
* **renovate:** port the Go analysis hash refresh to Go (RIG-8122) ([#59](https://github.com/RigelBuild/meissa/issues/59)) ([ab7ebe8](https://github.com/RigelBuild/meissa/commit/ab7ebe85f49ea92effba92938a97dfe628847176))


### Bug Fixes

* **nix:** expect toolchain-built tools in go-analysis check (RIG-8435) ([#64](https://github.com/RigelBuild/meissa/issues/64)) ([e8fead0](https://github.com/RigelBuild/meissa/commit/e8fead0a14449cda407c3f296b90cf0632c6bf5c))
* **renovate:** report interrupts from every refresh step (RIG-8122) ([#63](https://github.com/RigelBuild/meissa/issues/63)) ([ca8aa97](https://github.com/RigelBuild/meissa/commit/ca8aa97b116dd2ded02120dc72e9c739379872fa))

## 0.1.0 (2026-11-09)


### Features

* **go:** add the meissa Go module and port diff parsing (RIG-8118) ([#46](https://github.com/RigelBuild/meissa/issues/46)) ([385439d](https://github.com/RigelBuild/meissa/commit/385439d12d795c0d3b89ecbef4b7b72dcef91706))
* **go:** port ref-gate to Go and ship the flake tools package (RIG-8121) ([#51](https://github.com/RigelBuild/meissa/issues/51)) ([d75fe62](https://github.com/RigelBuild/meissa/commit/d75fe624874177b2758f28499d575fffdca67a1d))
* **go:** port the commentscan parser scanners and delete the TS package (RIG-8120) ([#49](https://github.com/RigelBuild/meissa/issues/49)) ([e33ea85](https://github.com/RigelBuild/meissa/commit/e33ea856e1ec467cc3062d348e66ba3ba82b5c56))
* **go:** port the commentscan scanners, part 1 (RIG-8119) ([#47](https://github.com/RigelBuild/meissa/issues/47)) ([dd0f0fe](https://github.com/RigelBuild/meissa/commit/dd0f0fe01ca5116af2112737a614c18c62a17380))
* **ledger:** port decision-file parsing and index rendering to Go (RIG-8123) ([#56](https://github.com/RigelBuild/meissa/issues/56)) ([f63744a](https://github.com/RigelBuild/meissa/commit/f63744a0629cd2b098090b1b505daee7a7e06a4c))

## Changelog

## Unreleased

### Changed

- Added the `ref-gate` CLI to the Go tools package. It rejects the JavaScript-supported `v` regular-expression flag; existing patterns must use supported flags.
- The Go Renovate hash refresher reports canceled child processes as signal interruptions.
- `DL_CLAIM_TOKEN_FILE` now fails with exit 2 when it exists but cannot be read, for example on permission denied or a directory path. The TypeScript CLI ignored these errors and fell back to `DL_CLAIM_TOKEN`.
- A failed base decision listing in the claim check reports `cannot read base decision listing`. The TypeScript CLI reported the claim-service message for it.
- The decision front-matter count reason is now `front matter must hold id, decision, status, record and optional title`.

### Added

- `code-shape-gate` measures Go and TypeScript functions, reports skipped files and unmeasured extensions, and detects suppression and Biome policy drift.
- Report unmeasured functions and Biome scope drift instead of silently passing them.


- Added a Renovate hash refresh command for Go source and vendor hashes, including the Go tools package vendor hash.
- Added `code-shape-gate`, which checks file length and directory width against a per-repo allowlist with a ratchet.
- `code-shape-gate` supports per-glob directory limits and excludes matching files from directory width counts.
- Added `length-gate`, the Go port of `@rigelbuild/length-gate` 1.0.2, with the same flags and environment variables.
  Invalid globs now fail config load with exit 2. The npm version accepted them at load and failed only when matching.
  Gap: a leading `!` in `exclude` or a class glob, and a fractional `target`, are now rejected at load with exit 2. The npm version accepted both.
- Added the `dl-claim`, `dl-list`, `dl-reconcile`, and `design-ledger-gate` Go binaries.
- `design-ledger-gate` supports one-level decision sub-areas, an optional `title:` front-matter key, and the `requireSubArea`, `titleScope` and `archiveRoots` config keys. A Record outside the designs root and every archive root reports `Record path must be under <root> or an archive root`. A live decision cannot be marked `Superseded by` an archived decision.

### Removed

- Removed the `@rigelbuild/length-gate` npm package. Its release-please config and manifest entries are deleted, and the binary ships in the Go tools package.
- Removed the `@rigelbuild/design-ledger-gate`, `@rigelbuild/dl-claim`, `@rigelbuild/dl-list`, and `@rigelbuild/dl-reconcile` npm packages.
