# Changelog

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

### Added

- `code-shape-gate` measures Go and TypeScript functions, reports skipped files and unmeasured extensions, and detects suppression and Biome policy drift.
- Report unmeasured functions and Biome scope drift instead of silently passing them.


- Added a Renovate hash refresh command for Go source and vendor hashes, including the Go tools package vendor hash.
- Added `code-shape-gate`, which checks file length and directory width against a per-repo allowlist with a ratchet.
- `code-shape-gate` supports per-glob directory limits and excludes matching files from directory width counts.
- Added `length-gate`, the Go port of `@rigelbuild/length-gate` 1.0.2, with the same flags and environment variables.
  Invalid globs now fail config load with exit 2. The npm version accepted them at load and failed only when matching.
  Gap: a leading `!` in `exclude` or a class glob, and a fractional `target`, are now rejected at load with exit 2. The npm version accepted both.

### Removed

- Removed the `@rigelbuild/length-gate` npm package. Its release-please config and manifest entries are deleted, and the binary ships in the Go tools package.
