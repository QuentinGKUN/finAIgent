# -*- coding: utf-8 -*-
import argparse
import json
import os
import re
import sys
import warnings
from datetime import datetime, timezone
from typing import Any

import requests


warnings.filterwarnings("ignore", message=".*doesn't match a supported version.*")


def emit_json(payload: dict[str, Any]) -> None:
    data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    sys.stdout.buffer.write(data)
    sys.stdout.write("\n")


def _as_int(value: Any) -> int | None:
    try:
        return int(str(value).strip())
    except Exception:
        return None


def _extract_symbol(title: str) -> str:
    text = str(title or "").strip()
    if not text:
        return ""
    patterns = [
        r"\b([A-Z]{2,6})\b",
        r"\b(\d{6}\.(?:SZ|SH|BJ|HK))\b",
        r"\b([A-Z]{1,5}\.[A-Z]{1,3})\b",
    ]
    for pat in patterns:
        m = re.search(pat, text)
        if m:
            return m.group(1).strip()
    return ""


def _normalize_category(raw: Any) -> str:
    c = str(raw or "").strip()
    if not c:
        return "市场观察"
    if c in {"A股", "港股", "美股", "行业研究", "市场观察", "个股探索", "宏观观察", "主题轮动"}:
        return c
    if "大盘" in c or "市场" in c:
        return "市场观察"
    if "行业" in c or "赛道" in c:
        return "行业研究"
    if "个股" in c or "公司" in c:
        return "个股探索"
    if "宏观" in c:
        return "宏观观察"
    if "主题" in c:
        return "主题轮动"
    if "港" in c:
        return "港股"
    if "美" in c:
        return "美股"
    if "A股" in c or "a股" in c:
        return "A股"
    return c[:24]


def fetch_hotspots(base_url: str, language: str, limit: int, timeout_sec: float) -> dict[str, Any]:
    base = str(base_url or "").strip().rstrip("/")
    if not base:
        base = "https://valuecell.ai/api/v1/leaderboard"
    source_url = f"{base}/?language={language}"
    resp = requests.get(
        source_url,
        headers={"User-Agent": "fin-assistant-hotspots/1.0"},
        timeout=(8, timeout_sec),
    )
    resp.raise_for_status()
    payload = resp.json()

    code = _as_int(payload.get("code"))
    if code not in (None, 0):
        raise RuntimeError(f"leaderboard code={code}")

    data = payload.get("data")
    entries = []
    if isinstance(data, dict):
        raw_entries = data.get("entries") or data.get("list") or []
        if isinstance(raw_entries, list):
            entries = raw_entries
    elif isinstance(data, list):
        entries = data
    if not entries and isinstance(payload.get("entries"), list):
        entries = payload.get("entries") or []

    items: list[dict[str, str]] = []
    seen: set[str] = set()
    for row in entries:
        if not isinstance(row, dict):
            continue
        title = str(row.get("title") or row.get("question") or "").strip()
        if not title:
            continue
        key = title.lower()
        if key in seen:
            continue
        seen.add(key)

        category = _normalize_category(row.get("custom_tag") or row.get("category"))
        heat = _as_int(row.get("heat_count"))
        symbol = _extract_symbol(title)
        if not symbol and heat is not None:
            symbol = f"热度{heat}"

        items.append(
            {
                "category": category,
                "title": title,
                "symbol": symbol,
            }
        )
        if len(items) >= limit:
            break

    return {
        "items": items,
        "source_url": source_url,
        "updated_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--limit", type=int, default=int(os.getenv("VALUECELL_HOTSPOT_LIMIT", "8")))
    parser.add_argument("--language", default=os.getenv("VALUECELL_HOTSPOT_LANG", "zh"))
    parser.add_argument("--api-url", default=os.getenv("VALUECELL_HOTSPOT_URL", "https://valuecell.ai/api/v1/leaderboard"))
    parser.add_argument("--timeout", type=float, default=float(os.getenv("VALUECELL_HOTSPOT_TIMEOUT_SEC", "25")))
    args = parser.parse_args()

    limit = max(1, min(20, int(args.limit or 8)))
    language = str(args.language or "zh").strip().lower()
    if not language:
        language = "zh"
    try:
        result = fetch_hotspots(
            base_url=str(args.api_url or ""),
            language=language,
            limit=limit,
            timeout_sec=max(8.0, float(args.timeout or 0)),
        )
        emit_json(result)
    except Exception as e:  # noqa: BLE001
        emit_json(
            {
                "items": [],
                "source_url": f"https://valuecell.ai/api/v1/leaderboard/?language={language}",
                "error": str(e),
            }
        )


if __name__ == "__main__":
    main()
