# -*- coding: utf-8 -*-
import argparse
import json
import os
import re
import sys
import uuid
import warnings

warnings.filterwarnings("ignore", message=".*doesn't match a supported version.*")

import requests


def emit_json(payload):
    data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    sys.stdout.buffer.write(data)
    sys.stdout.write("\n")


def _uniq(seq):
    out = []
    seen = set()
    for x in seq:
        k = str(x or "").strip()
        if not k or k in seen:
            continue
        seen.add(k)
        out.append(k)
    return out


def _expand_endpoint_candidates_for_base(base: str):
    b = str(base or "").strip().strip('"').strip("'").rstrip("/")
    if not b:
        return []
    if b.endswith("/agents/stream"):
        return [b]
    if re.search(r"/api/v1$", b, flags=re.IGNORECASE):
        root = re.sub(r"/api/v1$", "", b, flags=re.IGNORECASE).rstrip("/")
        return [
            b + "/agents/stream",
            root + "/api/v1/agents/stream",
            root + "/agents/stream",
        ]
    if re.search(r"/api$", b, flags=re.IGNORECASE):
        root = re.sub(r"/api$", "", b, flags=re.IGNORECASE).rstrip("/")
        return [
            b + "/v1/agents/stream",
            b + "/agents/stream",
            root + "/api/v1/agents/stream",
            root + "/agents/stream",
        ]
    return [
        b + "/api/v1/agents/stream",
        b + "/agents/stream",
        b + "/api/agents/stream",
    ]


def build_endpoint(base_url: str) -> str:
    cands = build_endpoint_candidates(base_url)
    if cands:
        return cands[0]
    return "http://127.0.0.1:8000/api/v1/agents/stream"


def build_endpoint_candidates(base_url: str):
    base = str(base_url or "").strip().strip('"').strip("'").rstrip("/")
    if not base:
        seed_bases = [
            "http://127.0.0.1:8000",
            "http://localhost:8000",
            "http://127.0.0.1:8010",
            "http://localhost:8010",
        ]
    else:
        seed_bases = [base]
        if "127.0.0.1" in base:
            seed_bases.append(base.replace("127.0.0.1", "localhost"))
        if "localhost" in base:
            seed_bases.append(base.replace("localhost", "127.0.0.1"))

    cands = []
    for b in _uniq(seed_bases):
        cands.extend(_expand_endpoint_candidates_for_base(b))
    return _uniq(cands)


def normalize_companies(raw: str):
    if not raw:
        return []
    return [x.strip() for x in str(raw).split(",") if x.strip()]


def compose_query(query: str, companies):
    text = (query or "").strip()
    if not companies:
        return text
    return (
        "请围绕以下公司进行深度分析，并优先输出可核验的数据来源与结论："
        + "、".join(companies)
        + "\n用户问题："
        + text
    )


def clip_text(text: str, max_len: int = 320):
    value = str(text or "").strip()
    if len(value) <= max_len:
        return value
    return value[: max_len - 3] + "..."


def _is_planner_provider_error(text: str) -> bool:
    s = str(text or "").strip().lower()
    if not s:
        return False
    patterns = [
        "planner is unavailable",
        "failed to initialize model/provider",
        "please configure a valid api key or provider settings",
        "failed to create model",
        "provider settings and retry",
        "planner encountered an error during execution",
        "planner selected unsupported agent(s)",
        "failed to resolve agent card",
        "error executing task",
    ]
    return any(p in s for p in patterns)


def parse_stream_line(line: str):
    if not line:
        return None
    s = line.strip()
    if not s or not s.startswith("data:"):
        return None
    body = s[5:].strip()
    if not body:
        return None
    if body == "[DONE]":
        return {"event": "done", "data": {"payload": {"content": ""}}}
    try:
        return json.loads(body)
    except Exception:
        return {"event": "message_chunk", "data": {"payload": {"content": body}}}


def parse_chunk(chunk):
    if isinstance(chunk, str):
        return {"event": "message_chunk", "content": chunk, "tool": "", "tool_result": ""}
    if not isinstance(chunk, dict):
        return {"event": "", "content": "", "tool": "", "tool_result": ""}

    event = str(chunk.get("event", "") or "").strip().lower()
    data = chunk.get("data")
    payload = data.get("payload") if isinstance(data, dict) else None

    content = ""
    tool = ""
    tool_result = ""
    if isinstance(payload, dict):
        content = str(payload.get("content", "") or "").strip()
        tool = str(payload.get("tool_name", "") or "").strip()
        tool_result = str(payload.get("tool_result", "") or "").strip()
    elif isinstance(payload, str):
        content = payload.strip()

    if not content and isinstance(data, dict):
        fallback = data.get("content")
        if isinstance(fallback, str):
            content = fallback.strip()

    return {"event": event, "content": content, "tool": tool, "tool_result": tool_result}


def run_query(api_url: str, query: str, companies, agent_name: str, timeout: float, conversation_id: str):
    endpoints = build_endpoint_candidates(api_url)
    payload = {
        "query": compose_query(query, companies),
        "conversation_id": conversation_id,
    }
    agent_name = str(agent_name or "").strip()
    if agent_name:
        payload["agent_name"] = agent_name
    headers = {
        "Accept": "text/event-stream",
        "Content-Type": "application/json",
    }
    token = os.getenv("VALUECELL_API_TOKEN", "").strip()
    if token:
        headers["Authorization"] = "Bearer " + token

    message_parts = []
    reasoning_parts = []
    tool_events = []
    errors = []

    probe_errors = []
    success_endpoint = ""
    for endpoint in endpoints:
        try:
            with requests.post(
                endpoint,
                json=payload,
                headers=headers,
                stream=True,
                timeout=(10, timeout),
            ) as resp:
                if resp.status_code >= 400:
                    body = clip_text(resp.text, 240)
                    probe_errors.append(f"{endpoint} -> {resp.status_code}: {body}")
                    # 4xx/5xx: try next candidate path
                    continue

                success_endpoint = endpoint
                for raw in resp.iter_lines(decode_unicode=True):
                    if raw is None:
                        continue
                    chunk = parse_stream_line(str(raw))
                    if chunk is None:
                        continue

                    item = parse_chunk(chunk)
                    event = item["event"]
                    content = item["content"]
                    tool = item["tool"]
                    tool_result = item["tool_result"]

                    if event == "done":
                        break
                    if event in {"message_chunk", "message"}:
                        if content:
                            message_parts.append(content)
                        continue
                    if event in {"reasoning", "reasoning_started", "reasoning_completed"}:
                        if content:
                            reasoning_parts.append(content)
                        continue
                    if event in {"tool_call_started", "tool_call_completed"}:
                        if tool:
                            if tool_result:
                                tool_events.append(f"{tool}: {clip_text(tool_result, 160)}")
                            else:
                                tool_events.append(tool)
                        elif content:
                            tool_events.append(clip_text(content, 160))
                        continue
                    if event in {"plan_failed", "system_failed", "task_failed"}:
                        if content:
                            errors.append(content)
                        continue
                    if event in {"thread_started", "task_started", "task_completed", "conversation_started"}:
                        continue
                    if content:
                        message_parts.append(content)
                # connected and streamed successfully
                break
        except requests.RequestException as e:
            probe_errors.append(f"{endpoint} -> {clip_text(str(e), 240)}")
            continue

    if not success_endpoint:
        joined = "; ".join(probe_errors[-4:]) if probe_errors else "no endpoint available"
        raise RuntimeError("valuecell endpoints unavailable: " + joined)

    analysis_text = "".join(message_parts).strip()
    if not analysis_text:
        analysis_text = "\n".join(reasoning_parts).strip()
    if errors and not analysis_text:
        raise RuntimeError("; ".join(errors))
    planner_fail = next((x for x in errors if _is_planner_provider_error(x)), "")
    if not planner_fail and _is_planner_provider_error(analysis_text):
        planner_fail = analysis_text
    if planner_fail:
        raise RuntimeError(clip_text(planner_fail, 420))
    if errors:
        analysis_text = analysis_text + "\n\n[系统提示]\n- " + "\n- ".join(errors)
    if not analysis_text:
        raise RuntimeError("valuecell stream returned empty content")

    return {
        "analysis_text": analysis_text,
        "tool_events": tool_events,
        "endpoint": success_endpoint,
        "source_url": "https://github.com/ValueCell-ai/valuecell",
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--query", required=True)
    parser.add_argument("--companies", default="")
    parser.add_argument("--api-url", default=os.getenv("VALUECELL_API_URL", "http://127.0.0.1:8010/api/v1"))
    parser.add_argument("--agent-name", default=os.getenv("VALUECELL_AGENT_NAME", ""))
    parser.add_argument("--conversation-id", default="")
    parser.add_argument("--timeout", type=float, default=float(os.getenv("VALUECELL_TIMEOUT_SEC", "180")))
    args = parser.parse_args()

    try:
        try:
            sys.stdout.reconfigure(encoding="utf-8")
        except Exception:
            pass
        companies = normalize_companies(args.companies)
        conversation_id = (args.conversation_id or "").strip() or ("fa-" + uuid.uuid4().hex)
        result = run_query(
            api_url=args.api_url,
            query=args.query,
            companies=companies,
            agent_name=(args.agent_name or "").strip(),
            timeout=max(20.0, float(args.timeout or 0)),
            conversation_id=conversation_id,
        )
        emit_json(result)
    except Exception as e:
        emit_json(
            {
                "analysis_text": "",
                "tool_events": [],
                "source_url": "https://github.com/ValueCell-ai/valuecell",
                "error": str(e),
            }
        )


if __name__ == "__main__":
    main()
