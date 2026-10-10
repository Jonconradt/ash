# site

Static site published to GitHub Pages (deployed by
[.github/workflows/site.yml](../.github/workflows/site.yml) on push to `main`
or on release). This is marketing/docs content only — it is not part of the Go
build.

- `index.html` — landing page.
- `docs.html` — usage/configuration documentation.
- `faq.html` — frequently asked questions.
- `languages.json` — language-picker labels, URL paths, and translation-service locale mapping.
- `generate_locales.py` — generates the locale pages from the English source pages.
- `site.js` — language switching and install-command copy behavior.
- `install.sh` — the `curl -fsSL .../install.sh | sh` one-line installer; downloads the latest release archive for the detected OS/arch and runs `ash install`.
- `styles.css` — shared stylesheet for the HTML pages.
- `favicon.svg` — site favicon.
- `CNAME` — custom domain configuration for GitHub Pages.
- `robots.txt`, `sitemap.xml` — search engine crawl/indexing hints.

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
run can resume without resending already-translated text. Delete that file to
discard the cache and request fresh machine translations.

To add a locale, first add its app catalog under
`../internal/localize/languages/`, then add its metadata to `languages.json`
(catalog locale, URL path, HTML/translation locale, and native language name).
Run `make site-locales` to refresh the picker and generate its pages. Generated
translations should be reviewed for technical accuracy and natural wording
before publishing.
