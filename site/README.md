# site

Static site published to GitHub Pages (deployed by
[.github/workflows/site.yml](../.github/workflows/site.yml) on push to `main`
or on release). This is marketing/docs content only — it is not part of the Go
build.

- `index.html` — landing page.
- `docs.html` — usage/configuration documentation.
- `faq.html` — frequently asked questions.
- `install.sh` — the `curl -fsSL .../install.sh | sh` one-line installer; downloads the latest release archive for the detected OS/arch and runs `ash install`.
- `styles.css` — shared stylesheet for the HTML pages.
- `favicon.svg` — site favicon.
- `CNAME` — custom domain configuration for GitHub Pages.
- `robots.txt`, `sitemap.xml` — search engine crawl/indexing hints.
