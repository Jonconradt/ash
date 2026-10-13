import unittest
from unittest.mock import patch

import generate_locales


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
