# -*- coding: utf-8 -*-
import argparse
import json
import math
import os
import re
import sys
from datetime import date, timedelta

import pandas as pd
import requests

# Force-disable system/environment proxy for AkShare requests.
# On Windows, requests/urllib may pick proxy from registry even when env is clean.
for _k in ["HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"]:
    os.environ.pop(_k, None)
os.environ["NO_PROXY"] = "*"
os.environ["no_proxy"] = "*"

_orig_session_init = requests.sessions.Session.__init__


def _session_init_no_proxy(self, *args, **kwargs):
    _orig_session_init(self, *args, **kwargs)
    self.trust_env = False


requests.sessions.Session.__init__ = _session_init_no_proxy

import akshare as ak

try:
    sys.stdout.reconfigure(encoding="utf-8")
except Exception:
    pass


def emit_json(payload: dict):
    data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    sys.stdout.buffer.write(data)
    sys.stdout.write("\n")


def normalize_code(raw: str) -> str:
    m = re.search(r"(\d{6})", str(raw or ""))
    return m.group(1) if m else str(raw or "").strip()


def parse_cn_number(v, ratio: bool = False):
    if v is None or isinstance(v, bool):
        return None
    if isinstance(v, (int, float)):
        if isinstance(v, float) and (math.isnan(v) or math.isinf(v)):
            return None
        return float(v) / 100.0 if ratio else float(v)

    s = str(v).strip().replace(",", "")
    if not s or s in {"--", "None", "nan", "NaN", "False", "True"}:
        return None

    mul = 1.0
    if s.endswith("%"):
        ratio = True
        s = s[:-1]
    if s.endswith("万亿"):
        mul = 1e12
        s = s[:-2]
    elif s.endswith("亿"):
        mul = 1e8
        s = s[:-1]
    elif s.endswith("万"):
        mul = 1e4
        s = s[:-1]
    elif s.endswith("元"):
        s = s[:-1]

    s = s.strip()
    try:
        num = float(s) * mul
    except Exception:
        return None
    return num / 100.0 if ratio else num


def build_revenue_yoy(series: dict):
    rev = series.get("Revenue") or []
    if len(rev) < 2:
        return
    rev_sorted = sorted(rev, key=lambda x: x["fy"])
    yoy = []
    for i in range(1, len(rev_sorted)):
        prev = rev_sorted[i - 1]
        cur = rev_sorted[i]
        pv = prev.get("value")
        cv = cur.get("value")
        if not pv:
            continue
        yoy.append(
            {
                "fy": int(cur["fy"]),
                "value": (float(cv) - float(pv)) / float(pv),
                "unit": "ratio",
            }
        )
    if yoy:
        series["RevenueYoY"] = yoy


def finance_mode(code: str, years: list[int]):
    raw = str(code or "").strip()
    symbol = normalize_code(raw)
    if not symbol:
        raise RuntimeError("invalid code")

    # HK symbol (e.g. 00700, 0700.HK): use HK analysis endpoint.
    raw_upper = raw.upper()
    if re.fullmatch(r"\d{4,5}(\.HK)?", raw_upper):
        hk_symbol = raw_upper.replace(".HK", "")
        hk_symbol = hk_symbol.zfill(5)
        return hk_finance_mode(symbol=hk_symbol, years=years)

    # US symbol (e.g. TSLA, AAPL): use AkShare US financial report endpoint.
    if re.fullmatch(r"[A-Za-z]{1,8}", raw):
        us = us_finance_mode(symbol=raw.upper(), years=years)
        return us

    df = ak.stock_financial_abstract_ths(symbol=symbol)
    if df is None or df.empty:
        raise RuntimeError("akshare finance data empty")
    if "报告期" not in df.columns:
        raise RuntimeError("akshare finance schema changed: missing 报告期")

    work = df.copy()
    work["报告期"] = pd.to_datetime(work["报告期"], errors="coerce")
    work = work.dropna(subset=["报告期"])
    if work.empty:
        raise RuntimeError("akshare finance no valid 报告期")

    work["FY"] = work["报告期"].dt.year.astype(int)
    if years:
        year_set = set(years)
        work = work[work["FY"].isin(year_set)]
    if work.empty:
        raise RuntimeError("akshare finance empty after year filter")

    metric_map = [
        ("营业总收入", "Revenue", False),
        ("净利润", "NetIncome", False),
        ("营业总收入同比增长率", "RevenueYoY", True),
        ("扣非净利润", "NetIncomeExcl", False),
    ]
    series = {}

    for fy, grp in work.groupby("FY"):
        g = grp.sort_values("报告期")
        y_end = g[g["报告期"].dt.strftime("%m-%d") == "12-31"]
        row = y_end.iloc[-1] if not y_end.empty else g.iloc[-1]

        for col, metric, ratio in metric_map:
            if col not in g.columns:
                continue
            val = parse_cn_number(row.get(col), ratio=ratio)
            if val is None:
                continue
            series.setdefault(metric, []).append(
                {
                    "fy": int(fy),
                    "value": float(val),
                    "unit": "ratio" if ratio else "CNY",
                }
            )

    for k in list(series.keys()):
        series[k] = sorted(series[k], key=lambda x: x["fy"])

    if not series:
        raise RuntimeError("akshare finance parsed series empty")
    if "RevenueYoY" not in series:
        build_revenue_yoy(series)

    return {
        "series": series,
        "source_url": "https://akshare.akfamily.xyz/data/stock/stock.html",
    }


def hk_finance_mode(symbol: str, years: list[int]):
    try:
        df = ak.stock_financial_hk_analysis_indicator_em(symbol=symbol)
    except Exception as e:
        raise RuntimeError(f"akshare hk finance query failed: {e}")
    if df is None or df.empty:
        raise RuntimeError("akshare hk finance data empty")
    if "REPORT_DATE" not in df.columns:
        raise RuntimeError("akshare hk finance schema changed: missing REPORT_DATE")

    work = df.copy()
    work["REPORT_DATE"] = pd.to_datetime(work["REPORT_DATE"], errors="coerce")
    work = work.dropna(subset=["REPORT_DATE"])
    if work.empty:
        raise RuntimeError("akshare hk finance no valid REPORT_DATE")

    # Keep annual rows when DATE_TYPE_CODE is present.
    if "DATE_TYPE_CODE" in work.columns:
        annual = work[work["DATE_TYPE_CODE"].astype(str).str.strip() == "001"]
        if not annual.empty:
            work = annual

    work["FY"] = work["REPORT_DATE"].dt.year.astype(int)
    if years:
        year_set = set(years)
        work = work[work["FY"].isin(year_set)]
    if work.empty:
        raise RuntimeError("akshare hk finance empty after year filter")

    currency = "HKD"
    if "CURRENCY" in work.columns:
        cands = work["CURRENCY"].astype(str).str.strip()
        cands = cands[cands != ""]
        if not cands.empty:
            currency = cands.iloc[0]

    metric_map = [
        ("OPERATE_INCOME", "Revenue", False),
        ("HOLDER_PROFIT", "NetIncome", False),
        ("OPERATE_INCOME_YOY", "RevenueYoY", True),
    ]
    series = {}

    for fy, grp in work.groupby("FY"):
        g = grp.sort_values("REPORT_DATE")
        row = g.iloc[-1]
        for col, metric, ratio in metric_map:
            if col not in g.columns:
                continue
            val = parse_cn_number(row.get(col), ratio=ratio)
            if val is None:
                continue
            series.setdefault(metric, []).append(
                {
                    "fy": int(fy),
                    "value": float(val),
                    "unit": "ratio" if ratio else currency,
                }
            )

    for k in list(series.keys()):
        series[k] = sorted(series[k], key=lambda x: x["fy"])

    if not series:
        raise RuntimeError("akshare hk finance parsed series empty")
    if "RevenueYoY" not in series:
        build_revenue_yoy(series)

    return {
        "series": series,
        "source_url": "https://akshare.akfamily.xyz/data/stock/stock.html",
    }


def us_finance_mode(symbol: str, years: list[int]):
    try:
        df = ak.stock_financial_us_report_em(
            stock=symbol, symbol="综合损益表", indicator="年报"
        )
    except Exception as e:
        raise RuntimeError(f"akshare us finance query failed: {e}")
    if df is None or df.empty:
        raise RuntimeError("akshare us finance data empty")
    if "REPORT_DATE" not in df.columns or "ITEM_NAME" not in df.columns or "AMOUNT" not in df.columns:
        raise RuntimeError("akshare us finance schema changed")

    work = df.copy()
    work["REPORT_DATE"] = pd.to_datetime(work["REPORT_DATE"], errors="coerce")
    work = work.dropna(subset=["REPORT_DATE"])
    if work.empty:
        raise RuntimeError("akshare us finance no valid REPORT_DATE")
    work["FY"] = work["REPORT_DATE"].dt.year.astype(int)
    if years:
        year_set = set(years)
        work = work[work["FY"].isin(year_set)]
    if work.empty:
        raise RuntimeError("akshare us finance empty after year filter")

    metric_name_candidates = {
        "Revenue": ["主营收入", "营业收入", "主营业务收入"],
        "NetIncome": ["净利润", "归属于母公司股东净利润", "归属于普通股股东净利润"],
        "OperatingIncome": ["营业利润"],
    }
    series = {}

    for metric, candidates in metric_name_candidates.items():
        sub = work[work["ITEM_NAME"].astype(str).isin(candidates)]
        if sub.empty:
            continue
        by_year = {}
        for fy, grp in sub.groupby("FY"):
            g = grp.sort_values("REPORT_DATE")
            row = g.iloc[-1]
            val = parse_cn_number(row.get("AMOUNT"), ratio=False)
            if val is None:
                continue
            by_year[int(fy)] = float(val)
        if by_year:
            series[metric] = [
                {"fy": y, "value": by_year[y], "unit": "USD"} for y in sorted(by_year.keys())
            ]

    if "Revenue" in series and len(series["Revenue"]) > 1:
        build_revenue_yoy(series)
    if not series:
        raise RuntimeError("akshare us finance parsed series empty")

    return {
        "series": series,
        "source_url": "https://akshare.akfamily.xyz/data/stock/stock.html",
    }


def month_start_end(year: int, month: int):
    if month < 1:
        month = 1
    if month > 12:
        month = 12
    start = date(year, month, 1)
    if month == 12:
        end = date(year + 1, 1, 1) - timedelta(days=1)
    else:
        end = date(year, month + 1, 1) - timedelta(days=1)
    return start, end


def board_mode(year: int, month: int, limit: int):
    start, end = month_start_end(year, month)
    names = ak.stock_board_industry_name_ths()
    if names is None or names.empty:
        raise RuntimeError("akshare board list empty")
    if "name" not in names.columns or "code" not in names.columns:
        raise RuntimeError("akshare board list schema changed")

    items = []
    if limit > 0:
        scan_count = min(len(names), max(limit * 3, 30))
        names = names.head(scan_count)
    for _, row in names.iterrows():
        name = str(row.get("name", "")).strip()
        code = str(row.get("code", "")).strip()
        if not name:
            continue
        try:
            hist = ak.stock_board_industry_index_ths(symbol=name)
        except Exception:
            continue
        if hist is None or hist.empty:
            continue
        if "日期" not in hist.columns or "收盘价" not in hist.columns:
            continue

        h = hist.copy()
        h["日期"] = pd.to_datetime(h["日期"], errors="coerce")
        h = h.dropna(subset=["日期"])
        h = h[(h["日期"] >= pd.Timestamp(start)) & (h["日期"] <= pd.Timestamp(end))]
        if h.shape[0] < 2:
            continue
        h = h.sort_values("日期")
        first = h.iloc[0]
        last = h.iloc[-1]
        start_close = parse_cn_number(first.get("收盘价"))
        end_close = parse_cn_number(last.get("收盘价"))
        if not start_close or not end_close:
            continue
        pct = (end_close / start_close - 1.0) * 100.0
        items.append(
            {
                "code": code,
                "name": name,
                "start_close": start_close,
                "end_close": end_close,
                "pct_change": pct,
                "start_date": first["日期"].strftime("%Y%m%d"),
                "end_date": last["日期"].strftime("%Y%m%d"),
            }
        )

    if not items:
        raise RuntimeError("akshare board interval data empty")

    items = sorted(items, key=lambda x: x["pct_change"], reverse=True)
    if limit > 0:
        items = items[:limit]

    return {
        "items": items,
        "source_url": "https://akshare.akfamily.xyz/data/stock/stock.html",
    }


def pick_col(df: pd.DataFrame, candidates: list[str]) -> str:
    cols = [str(c).strip() for c in df.columns]
    for c in candidates:
        if c in cols:
            return c
    return ""


def stock_rank_mode(limit: int, window: int):
    if limit <= 0:
        limit = 10
    if limit > 100:
        limit = 100
    if window <= 0:
        window = 1

    last_err = None
    df = None
    try:
        df = ak.stock_zh_a_spot_em()
    except Exception as e:
        last_err = e
    if df is None or df.empty:
        try:
            df = ak.stock_zh_a_spot()
        except Exception as e:
            last_err = e
    if df is None or df.empty:
        if last_err is not None:
            raise RuntimeError(str(last_err))
        raise RuntimeError("akshare stock snapshot empty")

    code_col = pick_col(df, ["代码", "symbol", "Symbol", "SECURITY_CODE", "code"])
    name_col = pick_col(df, ["名称", "name", "Name", "SECURITY_NAME_ABBR"])
    pct_col = pick_col(df, ["涨跌幅", "change_percent", "pct_chg", "CHANGE_RATE", "changepercent", "涨跌幅(%)"])
    price_col = pick_col(df, ["最新价", "最新", "price", "close", "最新价(元)", "trade", "现价"])
    amount_col = pick_col(df, ["成交额", "turnover", "amount", "成交金额"])

    if not code_col or not name_col or not pct_col:
        raise RuntimeError("akshare stock snapshot schema changed")

    items = []
    for _, row in df.iterrows():
        code = str(row.get(code_col, "")).strip()
        name = str(row.get(name_col, "")).strip()
        if not code or not name:
            continue
        pct = parse_cn_number(row.get(pct_col), ratio=False)
        if pct is None:
            continue
        latest = parse_cn_number(row.get(price_col), ratio=False) if price_col else None
        amount = parse_cn_number(row.get(amount_col), ratio=False) if amount_col else None
        items.append(
            {
                "code": code,
                "name": name,
                "pct_change": float(pct),
                "latest_price": latest,
                "turnover": amount,
            }
        )

    if not items:
        raise RuntimeError("akshare stock rank parsed items empty")

    items = sorted(items, key=lambda x: x["pct_change"], reverse=True)[:limit]
    period_label = "最新交易日"
    if window >= 5:
        period_label = f"最近{window}天（按最新交易日快照涨跌幅排序）"

    return {
        "items": items,
        "period_label": period_label,
        "source_url": "https://akshare.akfamily.xyz/data/stock/stock.html",
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--mode", choices=["finance", "board", "stock_rank"], required=True)
    parser.add_argument("--code", default="")
    parser.add_argument("--years", default="")
    parser.add_argument("--year", type=int, default=0)
    parser.add_argument("--month", type=int, default=0)
    parser.add_argument("--limit", type=int, default=0)
    parser.add_argument("--window", type=int, default=1)
    args = parser.parse_args()

    try:
        if args.mode == "finance":
            years = []
            if args.years.strip():
                for part in args.years.split(","):
                    part = part.strip()
                    if not part:
                        continue
                    years.append(int(part))
            payload = finance_mode(code=args.code, years=years)
        elif args.mode == "stock_rank":
            payload = stock_rank_mode(limit=args.limit, window=args.window)
        else:
            if args.year <= 0 or args.month <= 0:
                today = date.today()
                year = today.year
                month = today.month
            else:
                year = args.year
                month = args.month
            payload = board_mode(year=year, month=month, limit=args.limit)
        emit_json(payload)
    except Exception as e:
        emit_json({"error": str(e)})
        return


if __name__ == "__main__":
    main()
