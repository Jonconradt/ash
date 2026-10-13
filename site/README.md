# site

Static site published to GitHub Pages (deployed by
[.github/workflows/site.yml](../.github/workflows/site.yml) on pushes to `main`
that change `site/`, on pushes of release tags (`v*`), or on manually published
releases). This is marketing/docs content only — it is not part of the Go build.

- `index.html` — landing page.
- `install.html` — canonical OS-specific prerequisites, installation, providers, channels, updates, and uninstall guide; older installation anchors link here.
- `docs.html` — usage/configuration documentation.
- `faq.html` — frequently asked questions.
- `developer.html` — contributor setup, Make targets, release builders, diagnostics, and VM package tests.
- `languages.json` — language-picker labels, URL paths, and translation-service locale mapping.
- `generate_locales.py` — generates the locale pages from the English source pages.
- `site.js` — language switching and command copying with explicit clipboard-failure feedback.
- `install.sh` — alternative user-local installer; download and inspect it before executing. It verifies the latest Release archive for the detected OS/arch and runs `ash install`. Package managers are preferred.
- `apt/`, `rpm/`, and `pacman/` — the signed APT, DNF, and Pacman repositories, updated from published releases by [`.github/workflows/apt.yml`](../.github/workflows/apt.yml).
- `styles.css` — shared stylesheet for the HTML pages.
- `site.test.cjs` — dependency-free Node tests for command copying, language switching, guide structure, and local links; run by `make test`.
- `favicon.svg` — site favicon.
- `CNAME` — custom domain configuration for GitHub Pages.
- `robots.txt`, `sitemap.xml` — search engine crawl/indexing hints.

The Linux package repository workflow expects a `GPG_PRIVATE_KEY` repository secret containing the
base64-encoded output of `gpg --armor --export-secret-keys KEY_ID | base64 -w 0`.
Set an optional `GPG_PASSPHRASE` secret if that private key is protected by a
passphrase. On each published release, the workflow exports the matching public
key to `apt/gpg.key` and `rpm/gpg.key`, signs both APT and DNF repository
metadata, and commits the updated repository files to the default branch. RPM
package checksums are covered by the signed repository metadata; RPM files are
not individually signed. Arch packages and the Pacman repository database are
individually signed.

## Website translations

English is the source language and remains at the site root. Run
`make site-locales` after changing the English pages to regenerate all static
translated pages. To generate or refresh selected locales only, pass their
locale IDs or URL paths, for example
`make site-locales SITE_LOCALES="zh-TW lb_LU"`. Locale pages are written below
short language paths such as `fr/`, and the sitemap and language-picker options
are refreshed at the same time. Translations are generated at build time
through Google's public translation endpoint; visitors do not make
translation-service requests.

The generator caches completed translation requests in the ignored
`.site-translation-cache.json` file at the repository root, so an interrupted
run can resume without resending already-translated text. It also records a
source hash per locale and page, reusing the generated page without translating
it again when its English HTML is unchanged. Changed pages and missing outputs
are regenerated. Delete that file to discard the cache and request fresh machine
translations.

To add a locale, first add its app catalog under
`../internal/localize/languages/`, then add its metadata to `languages.json`
(catalog locale, URL path, HTML/translation locale, and native language name).
Run `make site-locales` to refresh the picker and generate its pages. Generated
translations should be reviewed for technical accuracy and natural wording
before publishing.

`make release` runs the same generator against a temporary copy of the site and
attaches the resulting complete localized site as
`ash-site-<version>.tar.gz`. This keeps release-time translation from modifying
the checked-in English pages, generated locale pages, sitemap, or local cache.
