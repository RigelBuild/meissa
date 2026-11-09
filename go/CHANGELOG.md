# Changelog

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
