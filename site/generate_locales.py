#!/usr/bin/env python3
"""Generate static website translations from the English source pages."""

from __future__ import annotations

import argparse
import copy
import hashlib
import html
import json
import re
import subprocess
import time
from dataclasses import dataclass, field
from html.parser import HTMLParser
from pathlib import Path


SITE_DIR = Path(__file__).resolve().parent
REPO_DIR = SITE_DIR.parent
BASE_URL = "https://jonconradt.github.io/ash/"
PAGES = ("index.html", "docs.html", "faq.html")
CATALOG_DIR = REPO_DIR / "internal" / "localize" / "languages"
CACHE_FILE = REPO_DIR / ".site-translation-cache.json"
CACHE_VERSION = 1
TRANSLATE_URL = "https://translate.googleapis.com/translate_a/t"
MAX_BATCH_LENGTH = 4200
BLOCK_TAGS = {
    "a",
    "button",
    "dd",
    "dt",
    "h1",
    "h2",
    "h3",
    "h4",
    "label",
    "li",
    "p",
    "span",
    "strong",
    "summary",
    "title",
}
VOID_TAGS = {
    "area",
    "base",
    "br",
    "col",
    "embed",
    "hr",
    "img",
    "input",
    "link",
    "meta",
    "param",
    "source",
    "track",
    "wbr",
}
SKIP_TAGS = {"code", "option", "pre", "script", "style", "textarea"}
LOCALE_OPTIONS_MARKER = "<!-- LANGUAGE_OPTIONS -->"
BRAND_PATTERN = re.compile(r"\b(ash|Ash|ASH)\b")
URL_PATTERN = re.compile(r"\b(?:https?://|www\.)[^\s<]+|\bgithub\.com/[^\s<]+")
TRANSLATION_CACHE: dict[str, str] = {}
UNSAVED_CACHE_ENTRIES = 0


@dataclass
class Node:
    tag: str | None = None
    start: str = ""
    end: str = ""
    text: str = ""
    children: list[Node] = field(default_factory=list)


class FragmentParser(HTMLParser):
    def __init__(self, source: str):
        super().__init__(convert_charrefs=False)
        self.root = Node()
        self.stack = [self.root]
        self.feed(source)
        self.close()

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        node = Node(tag=tag, start=self.get_starttag_text() or "")
        self.stack[-1].children.append(node)
        if tag not in VOID_TAGS:
            self.stack.append(node)

    def handle_startendtag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        self.stack[-1].children.append(Node(start=self.get_starttag_text() or ""))

    def handle_endtag(self, tag: str) -> None:
        for index in range(len(self.stack) - 1, 0, -1):
            if self.stack[index].tag == tag:
                self.stack[index].end = f"</{tag}>"
                del self.stack[index:]
                return

    def handle_data(self, data: str) -> None:
        self.stack[-1].children.append(Node(text=data))

    def handle_entityref(self, name: str) -> None:
        self.stack[-1].children.append(Node(text=f"&{name};"))

    def handle_charref(self, name: str) -> None:
        self.stack[-1].children.append(Node(text=f"&#{name};"))

    def handle_comment(self, data: str) -> None:
        self.stack[-1].children.append(Node(text=f"<!--{data}-->"))

    def handle_decl(self, decl: str) -> None:
        self.stack[-1].children.append(Node(text=f"<!{decl}>"))

    def handle_pi(self, data: str) -> None:
        self.stack[-1].children.append(Node(text=f"<?{data}>"))


def serialize(node: Node) -> str:
    if node.tag is None:
        return node.text + "".join(serialize(child) for child in node.children)
    return node.start + "".join(serialize(child) for child in node.children) + node.end


def is_block(node: Node) -> bool:
    return node.tag in BLOCK_TAGS and bool(node.children)


def translate_request(text: str, target: str) -> str:
    global UNSAVED_CACHE_ENTRIES
    key = hashlib.sha256(f"{target}\0{text}".encode("utf-8")).hexdigest()
    if key in TRANSLATION_CACHE:
        return TRANSLATION_CACHE[key]

    command = [
        "curl",
        "--fail",
        "--silent",
        "--show-error",
        "--get",
        TRANSLATE_URL,
        "--data-urlencode",
        "client=dict-chrome-ex",
        "--data-urlencode",
        "sl=en",
        "--data-urlencode",
        f"tl={target}",
        "--data-urlencode",
        "dt=t",
        "--data-urlencode",
        f"q={text}",
    ]
    for attempt in range(6):
        response = subprocess.run(
            command, capture_output=True, text=True, timeout=30, check=False
        )
        if response.returncode:
            status_match = re.search(
                r"returned error: (\d+)", response.stderr
            )
            status_code = int(status_match.group(1)) if status_match else 0
            if status_code == 429 or status_code >= 500:
                if attempt < 5:
                    time.sleep(min(2**attempt, 30))
                    continue
            raise RuntimeError(
                f"Translation request failed for {target}: {response.stderr.strip()}"
            )
        payload = json.loads(response.stdout)
        if payload and isinstance(payload[0], str):
            translated = payload[0]
        else:
            chunks = payload[0]
            translated = "".join(
                chunk[0]
                for chunk in chunks
                if chunk and isinstance(chunk[0], str)
            )
        if not translated:
                raise RuntimeError(
                    f"Translation service returned no text for {target}: {text[:120]!r}"
                )
        TRANSLATION_CACHE[key] = translated
        UNSAVED_CACHE_ENTRIES += 1
        if UNSAVED_CACHE_ENTRIES >= 10:
            save_translation_cache()
        time.sleep(0.25)
        return translated
    raise RuntimeError(f"Translation service failed for {target}")


def parse_fragment(fragment: str) -> list[Node]:
    return FragmentParser(fragment).root.children


def load_translation_cache() -> None:
    if not CACHE_FILE.exists():
        return
    data = json.loads(CACHE_FILE.read_text(encoding="utf-8"))
    if data.get("version") != CACHE_VERSION or not isinstance(
        data.get("translations"), dict
    ):
        raise RuntimeError(f"Unsupported translation cache format: {CACHE_FILE}")
    TRANSLATION_CACHE.update(data["translations"])


def save_translation_cache() -> None:
    global UNSAVED_CACHE_ENTRIES
    temporary = CACHE_FILE.with_suffix(".json.tmp")
    temporary.write_text(
        json.dumps(
            {"version": CACHE_VERSION, "translations": TRANSLATION_CACHE},
            ensure_ascii=False,
            sort_keys=True,
        )
        + "\n",
        encoding="utf-8",
    )
    temporary.replace(CACHE_FILE)
    UNSAVED_CACHE_ENTRIES = 0


def make_token(replacements: dict[str, str], value: str) -> str:
    index = len(replacements)
    token = f"[QZCODE_{index}_XQ]"
    while token in value or token in replacements:
        index += 1
        token = f"[QZCODE_{index}_XQ]"
    replacements[token] = value
    return token


def protect_text(text: str, replacements: dict[str, str]) -> str:
    text = URL_PATTERN.sub(
        lambda match: make_token(replacements, match.group(0)), text
    )
    return BRAND_PATTERN.sub(
        lambda match: make_token(replacements, match.group(0)), text
    )


def has_translatable_text(text: str) -> bool:
    return bool(re.sub(r"\[QZCODE_\d+_XQ\]", "", text).strip())


def protect_nodes(nodes: list[Node]) -> tuple[list[Node], dict[str, str]]:
    replacements: dict[str, str] = {}

    def protect(node: Node) -> list[Node]:
        if node.tag in {"code", "pre"}:
            return [Node(text=make_token(replacements, serialize(node)))]
        if node.tag is None:
            if node.text.startswith("<"):
                return [node]
            node.text = protect_text(node.text, replacements)
            return [node]
        children = []
        for child in node.children:
            children.extend(protect(child))
        node.children = children
        return [node]

    protected = []
    for node in copy.deepcopy(nodes):
        protected.extend(protect(node))
    return protected, replacements


def restore_tokens(
    nodes: list[Node], replacements: dict[str, str], target: str
) -> list[Node]:
    found = set()

    def restore(node: Node) -> list[Node]:
        if node.tag is None:
            result = []
            text = node.text
            while text:
                matches = [
                    (text.find(token), token)
                    for token in replacements
                    if token in text
                ]
                matches = [(position, token) for position, token in matches if position >= 0]
                if not matches:
                    result.append(Node(text=text))
                    break
                position, token = min(matches)
                if position:
                    result.append(Node(text=text[:position]))
                restored = replacements[token]
                result.extend(parse_fragment(restored) if restored.startswith("<") else [Node(text=restored)])
                found.add(token)
                text = text[position + len(token) :]
            return result
        children = []
        for child in node.children:
            children.extend(restore(child))
        node.children = children
        return [node]

    restored = []
    for node in nodes:
        restored.extend(restore(node))
    missing = set(replacements) - found
    if missing:
        raise RuntimeError(
            f"Translation altered protected text for {target}: "
            f"{[(token, replacements[token]) for token in sorted(missing)]!r}; "
            f"output={''.join(serialize(node) for node in restored)[:500]!r}"
        )
    return restored


def repair_tokens(nodes: list[Node], replacements: dict[str, str]) -> None:
    for token in replacements:
        pattern = re.compile(re.escape(token[:-2]) + r"Q?[A-Za-z]?\]")
        locations = []

        def find(node: Node) -> None:
            if node.tag is None:
                if node.children:
                    for child in node.children:
                        find(child)
                    return
                locations.extend((node, match) for match in pattern.finditer(node.text))
                return
            for child in node.children:
                find(child)

        find(Node(children=nodes))
        if len(locations) == 1:
            node, match = locations[0]
            node.text = (
                node.text[: match.start()] + token + node.text[match.end() :]
            )


def translated_plain_text(text: str, target: str) -> str:
    replacements: dict[str, str] = {}
    protected = protect_text(text, replacements)
    translated = (
        translate_request(protected, target)
        if has_translatable_text(protected)
        else protected
    )
    node = Node(text=translated)
    repair_tokens([node], replacements)
    for token, value in replacements.items():
        if token not in node.text:
            raise RuntimeError(f"Translation altered protected text for {target}")
        node.text = node.text.replace(token, value)
    return node.text


def translate_text_nodes(node: Node, target: str) -> None:
    if node.tag is None:
        if not node.text.strip() or node.text.startswith(("<", "&")):
            return
        replacements: dict[str, str] = {}
        source = protect_text(node.text, replacements)
        if not has_translatable_text(source):
            for token, value in replacements.items():
                source = source.replace(token, value)
            node.text = source
            return
        for attempt in range(3):
            translated = Node(text=translate_request(source, target))
            repair_tokens([translated], replacements)
            if all(token in translated.text for token in replacements):
                for token, value in replacements.items():
                    translated.text = translated.text.replace(token, value)
                node.text = translated.text
                return
            if attempt < 2:
                time.sleep(2**attempt)
        raise RuntimeError(
            f"Translation altered protected text in an HTML text node for {target}"
        )
    if node.tag in SKIP_TAGS:
        return
    for child in node.children:
        translate_text_nodes(child, target)


def needs_text_fallback(node: Node) -> bool:
    pending = [node]
    code_count = 0
    while pending:
        current = pending.pop()
        if current.tag == "a":
            return True
        if current.tag == "code":
            code_count += 1
            if code_count > 1:
                return True
        pending.extend(current.children)
    return False


def translate_group(
    nodes: list[Node], target: str, retry: int = 0
) -> list[Node]:
    if any(needs_text_fallback(node) for node in nodes):
        translated = []
        for node in nodes:
            fallback = copy.deepcopy(node)
            translate_text_nodes(fallback, target)
            translated.append(fallback)
        return translated

    if len(nodes) == 1 and nodes[0].tag == "title":
        title = nodes[0]
        text = html.unescape("".join(serialize(child) for child in title.children))
        title.children = [Node(text=html.escape(translated_plain_text(text, target)))]
        return [title]

    source = "".join(serialize(node) for node in nodes)
    if len(source) > MAX_BATCH_LENGTH:
        if len(nodes) == 1:
            raise RuntimeError(
                f"Translation block is too long ({len(source)} characters)"
            )
        midpoint = len(nodes) // 2
        return translate_group(nodes[:midpoint], target) + translate_group(
            nodes[midpoint:], target
        )

    protected_nodes, replacements = protect_nodes(nodes)
    source = "".join(serialize(node) for node in protected_nodes)
    translated = parse_fragment(translate_request(source, target))
    translated_blocks = [node for node in translated if is_block(node)]
    if len(translated_blocks) != len(nodes):
        if len(nodes) == 1:
            if retry < 2:
                return translate_group(nodes, target, retry + 1)
            raise RuntimeError(
                f"Translation changed HTML structure for target {target}: "
                f"source={source[:300]!r}, translated="
                f"{''.join(serialize(node) for node in translated)[:300]!r}"
            )
        midpoint = len(nodes) // 2
        return translate_group(nodes[:midpoint], target) + translate_group(
            nodes[midpoint:], target
        )
    for translated_node, source_node in zip(translated_blocks, nodes):
        if translated_node.children and translated_node.children[-1].tag is None:
            tail = translated_node.children[-1]
            incomplete_close = re.search(r"</[A-Za-z]*\s*$", tail.text)
            if incomplete_close:
                tail.text = tail.text[: incomplete_close.start()]
                if not tail.text:
                    translated_node.children.pop()
        translated_node.tag = source_node.tag
        translated_node.start = source_node.start
        translated_node.end = source_node.end
    repair_tokens(translated_blocks, replacements)
    serialized_output = "".join(serialize(node) for node in translated_blocks)
    if any(token not in serialized_output for token in replacements):
        if len(nodes) > 1:
            midpoint = len(nodes) // 2
            return translate_group(nodes[:midpoint], target) + translate_group(
                nodes[midpoint:], target
            )
        if retry < 2:
            return translate_group(nodes, target, retry + 1)
        fallback = copy.deepcopy(nodes[0])
        translate_text_nodes(fallback, target)
        return [fallback]
    return restore_tokens(translated_blocks, replacements, target)


def translate_tree(node: Node, target: str) -> None:
    index = 0
    while index < len(node.children):
        child = node.children[index]
        if is_block(child):
            group = [child]
            end = index + 1
            while end < len(node.children):
                next_node = node.children[end]
                if next_node.tag is None and not next_node.text.strip():
                    end += 1
                    continue
                if is_block(next_node):
                    group.append(next_node)
                    end += 1
                    continue
                break
            translated = translate_group(group, target)
            group_index = 0
            for position in range(index, end):
                if is_block(node.children[position]):
                    node.children[position] = translated[group_index]
                    group_index += 1
            index = end
            continue
        if child.tag not in SKIP_TAGS:
            translate_tree(child, target)
        index += 1


def update_picker(html_text: str, languages: list[dict], selected_path: str) -> str:
    options = []
    for language in languages:
        value = language["path"] or "en"
        selected = " selected" if language["path"] == selected_path else ""
        options.append(
            f'        <option value="{html.escape(value, quote=True)}"{selected}>'
            f'{html.escape(language["name"])}</option>'
        )
    option_html = "\n" + "\n".join(options) + "\n        "
    pattern = re.compile(
        r'(<select id="site-language"[^>]*>).*?(</select>)',
        flags=re.DOTALL,
    )
    result, count = pattern.subn(
        lambda match: match.group(1) + option_html + match.group(2),
        html_text,
        count=1,
    )
    if count != 1:
        raise RuntimeError(
            "Expected exactly one language picker per page; "
            f"found {count}, select-context="
            f"{html_text[html_text.find('<select') : html_text.find('<select') + 180]!r}"
        )
    return result.replace(LOCALE_OPTIONS_MARKER, "")


def translate_attributes(html_text: str, target: str) -> str:
    attr_pattern = re.compile(
        r'\b(aria-label|alt|data-copied-label)="([^"]*)"'
    )

    def translate_attribute(match: re.Match) -> str:
        value = html.unescape(match.group(2))
        return f'{match.group(1)}="{html.escape(translated_plain_text(value, target), quote=True)}"'

    html_text = attr_pattern.sub(translate_attribute, html_text)
    meta_pattern = re.compile(r"<meta\b[^>]*>", flags=re.IGNORECASE)

    def translate_meta(match: re.Match) -> str:
        tag = match.group(0)
        if not re.search(
            r'(?:name|property)="(?:description|og:description|twitter:description)"',
            tag,
            flags=re.IGNORECASE,
        ):
            return tag
        content_pattern = re.compile(r'\bcontent="([^"]*)"')
        return content_pattern.sub(
            lambda content: (
                'content="'
                + html.escape(
                    translated_plain_text(html.unescape(content.group(1)), target),
                    quote=True,
                )
                + '"'
            ),
            tag,
            count=1,
        )

    return meta_pattern.sub(translate_meta, html_text)


def localized_url(language: dict, page: str) -> str:
    path = language["path"]
    suffix = "" if page == "index.html" else page
    return BASE_URL + (f"{path}/" if path else "") + suffix


def set_locale_urls(html_text: str, language: dict, page: str, languages: list[dict]) -> str:
    page_url = localized_url(language, page)
    html_text = re.sub(
        r'(<html\b[^>]*\blang=")[^"]+(")',
        rf"\g<1>{language['htmlLang']}\2",
        html_text,
        count=1,
    )
    html_text = re.sub(
        r'(<link rel="canonical" href=")[^"]+(")',
        rf"\g<1>{page_url}\2",
        html_text,
        count=1,
    )
    html_text = re.sub(
        r'(<meta property="og:url" content=")[^"]+(")',
        rf"\g<1>{page_url}\2",
        html_text,
        count=1,
    )
    alternate_links = [
        f'  <link rel="alternate" hreflang="{entry["htmlLang"]}" '
        f'href="{localized_url(entry, page)}">'
        for entry in languages
    ]
    alternate_links.append(
        f'  <link rel="alternate" hreflang="x-default" '
        f'href="{localized_url(languages[0], page)}">'
    )
    return html_text.replace(
        "  <link rel=\"stylesheet\"",
        "\n".join(alternate_links) + "\n  <link rel=\"stylesheet\"",
        1,
    )


def set_asset_paths(html_text: str) -> str:
    return (
        html_text.replace('href="favicon.svg"', 'href="../favicon.svg"')
        .replace('href="styles.css"', 'href="../styles.css"')
        .replace('src="site.js"', 'src="../site.js"')
    )


def load_languages() -> list[dict]:
    languages = json.loads((SITE_DIR / "languages.json").read_text(encoding="utf-8"))
    catalog_locales = {
        path.stem for path in CATALOG_DIR.glob("*.json") if path.stem != "README"
    }
    configured_locales = {language["locale"] for language in languages}
    if catalog_locales != configured_locales:
        missing = sorted(catalog_locales - configured_locales)
        unknown = sorted(configured_locales - catalog_locales)
        raise RuntimeError(
            f"languages.json must match app locales; missing={missing}, unknown={unknown}"
        )
    if not languages or languages[0]["locale"] != "en_US":
        raise RuntimeError("English (en_US) must be the first configured language")
    paths = [language["path"] for language in languages]
    if len(paths) != len(set(paths)) or any(not path for path in paths[1:]):
        raise RuntimeError("Each non-English language needs a unique URL path")
    return languages


def write_sitemap(languages: list[dict]) -> None:
    urls = []
    for language in languages:
        urls.extend(localized_url(language, page) for page in PAGES)
    entries = "\n".join(f"  <url><loc>{url}</loc></url>" for url in urls)
    sitemap = (
        '<?xml version="1.0" encoding="UTF-8"?>\n'
        '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n'
        f"{entries}\n"
        "</urlset>\n"
    )
    (SITE_DIR / "sitemap.xml").write_text(sitemap, encoding="utf-8")


def generate_locale(
    language: dict, sources: dict[str, str], languages: list[dict]
) -> None:
    target = language["translationLang"]
    for page, source in sources.items():
        tree = FragmentParser(source).root
        translate_tree(tree, target)
        translated = translate_attributes(serialize(tree), target)
        translated = set_asset_paths(translated)
        translated = update_picker(translated, languages, language["path"])
        translated = set_locale_urls(translated, language, page, languages)
        output_dir = SITE_DIR / language["path"]
        output_dir.mkdir(parents=True, exist_ok=True)
        (output_dir / page).write_text(translated, encoding="utf-8")
    print(f"Generated {language['locale']} ({language['path']}/)")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--locale",
        action="append",
        help="Generate or refresh one locale by its catalog name or URL path",
    )
    args = parser.parse_args()

    load_translation_cache()
    languages = load_languages()
    requested = set(args.locale or [])
    available = {
        name
        for language in languages[1:]
        for name in (language["locale"], language["path"])
    }
    unknown = requested - available
    if unknown:
        parser.error(f"unknown locale(s): {', '.join(sorted(unknown))}")

    sources = {}
    for page in PAGES:
        source = (SITE_DIR / page).read_text(encoding="utf-8")
        source = update_picker(source, languages, "")
        sources[page] = source
        (SITE_DIR / page).write_text(source, encoding="utf-8")

    selected = [
        language for language in languages[1:]
        if not requested
        or requested.intersection({language["locale"], language["path"]})
    ]

    for language in selected:
        generate_locale(language, sources, languages)
        save_translation_cache()

    if requested:
        for language in languages[1:]:
            if language not in selected:
                for page in PAGES:
                    output = SITE_DIR / language["path"] / page
                    if output.exists():
                        text = output.read_text(encoding="utf-8")
                        text = update_picker(text, languages, language["path"])
                        output.write_text(text, encoding="utf-8")

    write_sitemap(languages)


if __name__ == "__main__":
    main()
