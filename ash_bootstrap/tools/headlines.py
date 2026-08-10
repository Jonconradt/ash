#!/usr/bin/env python3
import argparse
import json
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET
from typing import Dict, Any, List, Optional


def build_ai_docs() -> str:
    return """Capabilities:
- Fetch recent news headlines from Google News RSS.
- Support topic-based searches via --query or the default top headlines feed.
- Filter results by language and country.

Arguments:
- --query TEXT: Optional topic or search phrase to search for.
- --limit N: Number of articles to return (default: 5).
- --language CODE: Two-letter language code such as en or fr (default: en).
- --country CODE: Two-letter country code such as US or GB (default: US).
- --ai-docs: Print this documentation and exit.

Return format:
- JSON object with keys:
  - status: success or error
  - query: requested topic or "Top Headlines"
  - count: number of articles returned
  - articles: array of article objects, each with headline, source, published_at, and url
  - error_message: present only when status is error

Usage guidance for the AI:
- Treat the output as structured JSON and parse it directly.
- If status is error, surface the error_message to the user.
- Use the article list for summarizing or ranking current events.
"""


def fetch_latest_news(
    query: Optional[str] = None,
    limit: int = 5,
    language: str = "en",
    country: str = "US"
) -> Dict[str, Any]:
    """
    Fetches latest news headlines via RSS and returns a structured dictionary
    tailored for LLM / AI tool consumption.

    Args:
        query: Optional topic or search term (e.g., 'artificial intelligence').
               If None, fetches top national/global headlines.
        limit: Number of news items to return (default 5).
        language: Two-letter language code (default 'en').
        country: Two-letter country code (default 'US').

    Returns:
        Dict formatted for clean AI parsing.
    """
    base_url = "https://news.google.com/rss"
    params = f"hl={language}-{country}&gl={country}&ceid={country}:{language}"
    
    if query:
        encoded_query = urllib.parse.quote(query)
        url = f"{base_url}/search?q={encoded_query}&{params}"
    else:
        url = f"{base_url}?{params}"

    headers = {
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
    }

    try:
        req = urllib.request.Request(url, headers=headers)
        with urllib.request.urlopen(req, timeout=10) as response:
            xml_data = response.read()

        root = ET.fromstring(xml_data)
        channel = root.find("channel")

        articles: List[Dict[str, str]] = []

        if channel is not None:
            for item in channel.findall("item")[:limit]:
                full_title = item.findtext("title", "No Title")
                link = item.findtext("link", "")
                pub_date = item.findtext("pubDate", "")
                
                source_elem = item.find("source")
                if source_elem is not None and source_elem.text:
                    source_name = source_elem.text
                elif " - " in full_title:
                    source_name = full_title.rsplit(" - ", 1)[-1]
                else:
                    source_name = "Unknown"

                headline = full_title.rsplit(" - ", 1)[0] if " - " in full_title else full_title

                articles.append({
                    "headline": headline,
                    "source": source_name,
                    "published_at": pub_date,
                    "url": link
                })

        return {
            "status": "success",
            "query": query or "Top Headlines",
            "count": len(articles),
            "articles": articles
        }

    except Exception as err:
        return {
            "status": "error",
            "error_message": str(err),
            "articles": []
        }


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Fetch news headlines and print AI-friendly JSON output."
    )
    parser.add_argument("--query", help="Optional topic or search phrase to look up.")
    parser.add_argument("--limit", type=int, default=5, help="Number of articles to return.")
    parser.add_argument("--language", default="en", help="Two-letter language code.")
    parser.add_argument("--country", default="US", help="Two-letter country code.")
    parser.add_argument(
        "--ai-docs",
        action="store_true",
        help="Print AI usage documentation and exit.",
    )

    args = parser.parse_args()

    if args.ai_docs:
        print(build_ai_docs())
        return

    news_output = fetch_latest_news(
        query=args.query,
        limit=args.limit,
        language=args.language,
        country=args.country,
    )
    print(json.dumps(news_output, indent=2))


if __name__ == "__main__":
    main()
