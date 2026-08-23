#!/usr/bin/env python3
import argparse
import importlib
import json
import os
import sys
from datetime import UTC, datetime


def load_yfinance_module():
    try:
        # When this script is named yfinance.py, Python may import this file
        # instead of the third-party package. Remove the script directory from
        # sys.path before importing to avoid module shadowing.
        script_dir = os.path.dirname(os.path.abspath(__file__))
        if script_dir in sys.path:
            sys.path.remove(script_dir)
        return importlib.import_module("yfinance")
    except ImportError:
        print(
            json.dumps(
                {
                    "status": "error",
                    "message": (
                        "Missing required library 'yfinance'. Install via: pip install yfinance"
                    ),
                }
            )
        )
        sys.exit(1)


def build_ai_docs() -> str:
    return """Capabilities:
- Fetch price and change data for one or more stock tickers.
- Returns structured JSON suitable for AI consumption and downstream summarization.

Arguments:
- TICKER [TICKER ...]: One or more ticker symbols such as AAPL MSFT GOOG.
- --ai-docs: Print this documentation and exit.

Return format:
- JSON object with keys:
  - timestamp_utc: ISO 8601 timestamp for the request
  - total_requested: number of requested tickers
  - data: array of per-ticker results
- Each per-ticker result contains:
  - symbol
  - status: success or error
  - price, currency, previous_close, change, change_percent when successful
  - error when unsuccessful

Usage guidance for the AI:
- Parse the JSON response and present the price data clearly.
- If a ticker returns status error, mention the error field to the user.
"""


def fetch_stock_data(tickers):
    yf = load_yfinance_module()
    results = []

    for symbol in tickers:
        clean_symbol = symbol.strip().upper()
        try:
            ticker = yf.Ticker(clean_symbol)
            fast_info = ticker.fast_info

            current_price = fast_info.last_price
            prev_close = fast_info.previous_close
            currency = fast_info.currency

            if current_price is None:
                results.append(
                    {
                        "symbol": clean_symbol,
                        "status": "error",
                        "error": ("Invalid symbol or price data unavailable."),
                    }
                )
                continue

            change = round(current_price - prev_close, 4) if prev_close is not None else None
            change_percent = (
                round((change / prev_close) * 100, 2) if change is not None and prev_close else None
            )

            results.append(
                {
                    "symbol": clean_symbol,
                    "status": "success",
                    "price": round(current_price, 2),
                    "currency": currency or "USD",
                    "previous_close": (round(prev_close, 2) if prev_close else None),
                    "change": change,
                    "change_percent": change_percent,
                }
            )
        except Exception as e:
            results.append(
                {
                    "symbol": clean_symbol,
                    "status": "error",
                    "error": str(e),
                }
            )

    return {
        "timestamp_utc": datetime.now(UTC).isoformat(),
        "total_requested": len(tickers),
        "data": results,
    }


def main():
    parser = argparse.ArgumentParser(
        description=("Fetch real-time stock prices formatted for AI tool responses.")
    )
    parser.add_argument(
        "tickers",
        nargs="*",
        metavar="TICKER",
        help="One or more ticker symbols (e.g., AAPL MSFT GOOG)",
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

    if not args.tickers:
        parser.error("the following arguments are required: TICKER")

    output = fetch_stock_data(args.tickers)
    print(json.dumps(output, indent=2))


if __name__ == "__main__":
    main()
