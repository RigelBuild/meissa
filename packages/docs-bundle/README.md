# @rigelbuild/docs-bundle

Shared helpers for collecting Markdown pages into a docs site, rewriting links, and checking publish rules before a build. The package does not fetch source repositories or invoke a site generator.

## Install

```sh
bun add -d @rigelbuild/docs-bundle
```

The package ships TypeScript source and runs on Bun.

## Check a repository

```sh
docs-bundle check [--root .] [--config required|default]
```

The default config mode is `required`. It expects `docs-bundle.toml` at the repository root. Use `--config default` when a source has no publish config; all gatherable Markdown files use `/<owner>/<repo>/<path>` routes.

The TOML file requires an `exclude` array. `[[sections]]` entries assign page prefixes or explicit files to site routes. An optional `[packages]` table configures package README routing and the allowed package pages.

`packages.readme_depth` is the maximum number of path segments for a README.md or AGENTS.md in the catch-all. `packages.allow` lists exact paths or directory prefixes ending in `/`. `packages.nested` lists top-level directories whose first two path segments form one package ID, joined with a hyphen.

The check runs on Markdown files and does not need a site generator. Exit codes:

| Code | Meaning |
| --- | --- |
| 0 | No errors. |
| 1 | Page collection failed (route collisions, stray or unassigned pages, an unreadable root), or broken relative links. |
| 2 | Invalid arguments, a missing or invalid `docs-bundle.toml`, or a page that cannot be read while checking links. |

## Library

```ts
import { collectPages, extractTitle, readPublishConfig, rewriteLinks } from "@rigelbuild/docs-bundle";

const config = await readPublishConfig(repositoryRoot, "required");
const pages = await collectPages(source, config);
const title = extractTitle(markdown, "docs/guide.md");
const rewritten = rewriteLinks(markdown, page, routes, repositoryRoot);
```

Only ATX `#` headings are read for titles; Setext headings are not supported.

`rewriteLinks` routes bundled pages to the site. Existing files outside the bundle and missing targets link to the source repository's GitHub blob URL; missing targets also call `onMissing` when supplied.
