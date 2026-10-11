# site

Static site published to GitHub Pages (deployed by
[.github/workflows/site.yml](../.github/workflows/site.yml) on pushes to `main`
that change `site/`, on pushes of release tags (`v*`), or on manually published
releases). This is marketing/docs content only — it is not part of the Go build.

- `index.html` — landing page.
- `docs.html` — usage/configuration documentation.
- `faq.html` — frequently asked questions.
- `install.sh` — the `curl -fsSL .../install.sh | sh` one-line installer; downloads the latest release archive for the detected OS/arch and runs `ash install`.
- `apt/` and `rpm/` — the signed APT and DNF repositories, updated from published releases by [`.github/workflows/apt.yml`](../.github/workflows/apt.yml).
- `styles.css` — shared stylesheet for the HTML pages.
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
not individually signed.
