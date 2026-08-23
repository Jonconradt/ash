#!/usr/bin/env python3
import argparse
import json
import time
import urllib.error
import urllib.parse
import urllib.request


def build_ai_docs() -> str:
    return """Capabilities:
- Search Wikipedia for a topic and return a concise summary.
- Use the first matching page and include a link to the page.

Arguments:
- --query TEXT: The search query to find on Wikipedia (default: quantum computing).
- --user-agent TEXT: Descriptive User-Agent header for respectful API usage.
- --ai-docs: Print this documentation and exit.

Return format:
- JSON object with keys:
  - status: success, not_found, or error
  - query: the original query
  - title: the page title when available
  - description: short article description when available
  - summary: article extract text when available
  - url: desktop page URL when available
  - message: human-readable detail for not_found or error cases

Usage guidance for the AI:
- Parse the JSON response and surface the summary text to the user.
- If status is not_found or error, explain the issue clearly.
"""


def make_request_with_backoff(url: str, headers: dict, max_retries: int = 3) -> dict:
    """Performs an HTTP GET request with automatic handling for HTTP 429 (Too Many Requests)."""
    for attempt in range(max_retries + 1):
        try:
            req = urllib.request.Request(url, headers=headers)
            with urllib.request.urlopen(req) as response:
                return json.loads(response.read().decode("utf-8"))
        except urllib.error.HTTPError as e:
            if e.code == 429:
                if attempt == max_retries:
                    raise e

                retry_after = e.headers.get("Retry-After")
                wait_time = (
                    int(retry_after)
                    if retry_after and retry_after.isdigit()
                    else (2 ** (attempt + 1))
                )
                time.sleep(wait_time)
            else:
                raise e
    raise Exception("Max retries exceeded")


def wikipedia_search_tool(
    query: str, user_agent: str = "MyAIAgent/1.0 (contact@example.com)"
) -> str:
    """Searches Wikipedia and returns the first result in a clean AI-friendly JSON format."""
    headers = {"User-Agent": user_agent}

    try:
        search_params = urllib.parse.urlencode(
            {"action": "query", "list": "search", "srsearch": query, "format": "json", "srlimit": 1}
        )
        search_url = f"https://en.wikipedia.org/w/api.php?{search_params}"
        search_data = make_request_with_backoff(search_url, headers)

        search_results = search_data.get("query", {}).get("search", [])
        if not search_results:
            return json.dumps(
                {
                    "status": "not_found",
                    "query": query,
                    "message": f"No Wikipedia pages found matching '{query}'.",
                },
                indent=2,
            )

        top_title = search_results[0]["title"]

        encoded_title = urllib.parse.quote(top_title, safe="")
        summary_url = f"https://en.wikipedia.org/api/rest_v1/page/summary/{encoded_title}"
        summary_data = make_request_with_backoff(summary_url, headers)

        tool_output = {
            "status": "success",
            "query": query,
            "title": summary_data.get("title", top_title),
            "description": summary_data.get("description", ""),
            "summary": summary_data.get("extract", ""),
            "url": summary_data.get("content_urls", {}).get("desktop", {}).get("page", ""),
        }
        return json.dumps(tool_output, indent=2, ensure_ascii=False)

    except urllib.error.HTTPError as e:
        return json.dumps(
            {
                "status": "error",
                "query": query,
                "error_code": e.code,
                "message": f"HTTP {e.code}: Rate limit or request failure ({e.reason})",
            },
            indent=2,
        )
    except Exception as e:
        return json.dumps({"status": "error", "query": query, "message": str(e)}, indent=2)


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Search Wikipedia and print AI-friendly JSON output."
    )
    parser.add_argument("--query", default="quantum computing", help="Search query to look up.")
    parser.add_argument(
        "--user-agent",
        default="MyAIAgent/1.0 (contact@example.com)",
        help="Descriptive User-Agent header for the request.",
    )
    parser.add_argument(
        "--ai-docs",
        action="store_true",
        help="Print AI usage documentation and exit.",
    )

    args = parser.parse_args()

    if args.ai_docs:
        print(build_ai_docs())
        return

    output = wikipedia_search_tool(args.query, args.user_agent)
    print(output)


if __name__ == "__main__":
    main()
