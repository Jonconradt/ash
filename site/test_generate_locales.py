import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import generate_locales


class GenerateLocaleCacheTests(unittest.TestCase):
    def setUp(self):
        self.translations = generate_locales.TRANSLATION_CACHE.copy()
        self.page_hashes = generate_locales.PAGE_TRANSLATION_HASHES.copy()
        self.unsaved_entries = generate_locales.UNSAVED_CACHE_ENTRIES
        generate_locales.TRANSLATION_CACHE.clear()
        generate_locales.PAGE_TRANSLATION_HASHES.clear()
        generate_locales.UNSAVED_CACHE_ENTRIES = 0

    def tearDown(self):
        generate_locales.TRANSLATION_CACHE.clear()
        generate_locales.TRANSLATION_CACHE.update(self.translations)
        generate_locales.PAGE_TRANSLATION_HASHES.clear()
        generate_locales.PAGE_TRANSLATION_HASHES.update(self.page_hashes)
        generate_locales.UNSAVED_CACHE_ENTRIES = self.unsaved_entries

    def test_reuses_unchanged_pages_and_regenerates_changed_or_missing_pages(self):
        source = (
            '<html lang="en"><head><link rel="stylesheet" href="styles.css">'
            "</head><body><select id=\"site-language\">"
            "<!-- LANGUAGE_OPTIONS --></select><p>Original</p></body></html>"
        )
        language = {
            "locale": "fr_FR",
            "translationLang": "fr",
            "path": "fr",
            "htmlLang": "fr",
            "name": "Français",
        }
        english = {
            "locale": "en_US",
            "translationLang": "en",
            "path": "",
            "htmlLang": "en",
            "name": "English",
        }

        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with (
                patch.object(generate_locales, "SITE_DIR", root),
                patch.object(generate_locales, "CACHE_FILE", root / "cache.json"),
                patch.object(generate_locales, "translate_tree") as translate_tree,
            ):
                generate_locales.generate_locale(language, {"index.html": source}, [english, language])
                translate_tree.assert_called_once()
                generate_locales.save_translation_cache()

                generate_locales.PAGE_TRANSLATION_HASHES.clear()
                generate_locales.load_translation_cache()
                translate_tree.reset_mock()
                spanish = {
                    "locale": "es_ES",
                    "translationLang": "es",
                    "path": "es",
                    "htmlLang": "es",
                    "name": "Español",
                }
                generate_locales.generate_locale(
                    language, {"index.html": source}, [english, language, spanish]
                )
                translate_tree.assert_not_called()
                output = (root / "fr" / "index.html").read_text(encoding="utf-8")
                self.assertIn('<option value="es">Español</option>', output)

                changed_source = source.replace("Original", "Updated")
                generate_locales.generate_locale(
                    language, {"index.html": changed_source}, [english, language]
                )
                translate_tree.assert_called_once()

                (root / "fr" / "index.html").unlink()
                translate_tree.reset_mock()
                generate_locales.generate_locale(
                    language, {"index.html": changed_source}, [english, language]
                )
                translate_tree.assert_called_once()
                self.assertTrue((root / "fr" / "index.html").is_file())


class TranslateGroupMarkupTests(unittest.TestCase):
    def test_malformed_outer_tag_falls_back_to_text_translation(self):
        calls = []

        def translate(text, target, force=False):
            calls.append(text)
            if text.startswith("<th"):
                return '<thscope="col">المزود'
            return "المزوّد"

        source = generate_locales.parse_fragment('<th scope="col">Provider</th>')
        with patch.object(generate_locales, "translate_request", side_effect=translate):
            translated = generate_locales.translate_group(source, "ar")

        self.assertEqual(
            "".join(generate_locales.serialize(node) for node in translated),
            '<th scope="col">المزوّد</th>',
        )
        self.assertEqual(calls.count('<th scope="col">Provider</th>'), 3)

    def test_malformed_nested_tag_falls_back_without_changing_markup(self):
        def translate(text, target, force=False):
            if text.startswith("<p"):
                return "<p><em>مطلوب</em></p>"
            return "مطلوب"

        source = generate_locales.parse_fragment(
            "<p><strong>Required</strong></p>"
        )
        with patch.object(generate_locales, "translate_request", side_effect=translate):
            translated = generate_locales.translate_group(source, "ar")

        self.assertEqual(
            "".join(generate_locales.serialize(node) for node in translated),
            "<p><strong>مطلوب</strong></p>",
        )


if __name__ == "__main__":
    unittest.main()
