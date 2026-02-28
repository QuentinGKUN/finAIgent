import { useEffect, useRef, useState } from "react";
import Chart from "chart.js/auto";

const API_BASE = import.meta.env.VITE_API_BASE || "http://localhost:3000/api";
const API_ORIGIN = (() => {
  try {
    return new URL(API_BASE, window.location.origin).origin;
  } catch (_e) {
    return window.location.origin;
  }
})();

const WELCOME_HTML =
  "你好！我可以做：<br/>" +
  "1) 年报证据问答（10-K/20-F）<br/>" +
  "2) 财务趋势图表与公司对比<br/>" +
  "3) 国内榜单与Top10图表<br/>" +
  "4) 数据对比：优先走 AkShare + 美股接口取数，再生成对比表和总结<br/>" +
  "5) 深度分析：调用 ValueCell 进行公司深度研究与总结<br/><br/>" +
  "直接提问即可。";

const newId = () => `${Date.now()}-${Math.random().toString(16).slice(2)}`;

function esc(input = "") {
  return String(input).replace(
    /[&<>"']/g,
    (m) =>
      ({
        "&": "&amp;",
        "<": "&lt;",
        ">": "&gt;",
        '"': "&quot;",
        "'": "&#039;"
      })[m]
  );
}

function progressStageLabel(stage = "") {
  const s = String(stage || "").trim().toLowerCase();
  if (s === "received") return "请求接收";
  if (s === "memory") return "记忆整理";
  if (s === "route") return "路由判断";
  if (s === "compare_plan") return "对比规划";
  if (s === "compare_query") return "对比查询";
  if (s === "compare_render") return "图表生成";
  if (s === "stock_rank") return "个股排行查询";
  if (s === "board") return "板块查询";
  if (s === "finance_target") return "标的识别";
  if (s === "finance_query") return "财务查询";
  if (s === "finance_summary") return "财务总结";
  if (s === "deep_plan") return "深度分析规划";
  if (s === "deep_query") return "深度研究查询";
  if (s === "deep_summary") return "深度分析总结";
  if (s === "llm_summary") return "模型总结";
  if (s === "llm") return "模型生成";
  if (s === "done") return "完成";
  if (s === "idle") return "空闲";
  return "处理中";
}

function stageProgressPercent(stage = "") {
  const s = String(stage || "").trim().toLowerCase();
  if (s === "received") return 5;
  if (s === "memory") return 12;
  if (s === "route") return 20;
  if (s === "compare_plan") return 30;
  if (s === "compare_query") return 45;
  if (s === "compare_render") return 62;
  if (s === "stock_rank" || s === "board") return 55;
  if (s === "finance_target") return 35;
  if (s === "finance_query") return 58;
  if (s === "finance_summary") return 72;
  if (s === "deep_plan") return 28;
  if (s === "deep_query") return 62;
  if (s === "deep_summary") return 86;
  if (s === "llm_summary") return 82;
  if (s === "llm") return 88;
  if (s === "done" || s === "idle") return 100;
  return 15;
}

function formatElapsed(ms = 0) {
  const totalSec = Math.max(0, Math.floor(Number(ms || 0) / 1000));
  const min = Math.floor(totalSec / 60);
  const sec = totalSec % 60;
  if (min <= 0) return `${sec}s`;
  return `${min}m ${sec}s`;
}

function formatHotspotUpdatedAt(raw = "") {
  if (!raw) return "";
  const t = new Date(raw);
  if (Number.isNaN(t.getTime())) return "";
  return t.toLocaleTimeString("zh-CN", { hour12: false });
}

function buildProgressHtml(progress = {}, localElapsedMs = 0) {
  const detail = esc(progress.detail || "正在处理中...");
  const stage = esc(progressStageLabel(progress.stage || ""));
  const raw = Number(progress.progress);
  const pct = Number.isFinite(raw)
    ? Math.max(0, Math.min(100, Math.round(raw)))
    : stageProgressPercent(progress.stage || "");
  const serverElapsedMs = Number(progress.elapsedMs);
  const clientElapsedMs = Number(localElapsedMs || 0);
  const elapsedMs = Math.max(
    Number.isFinite(serverElapsedMs) ? serverElapsedMs : 0,
    Number.isFinite(clientElapsedMs) ? clientElapsedMs : 0
  );
  const elapsed = esc(formatElapsed(elapsedMs));
  return `
    <div class="text-slate-600">${detail}</div>
    <div class="text-xs text-slate-400 mt-1">阶段：${stage}</div>
    <div class="mt-2">
      <div class="h-2 bg-slate-200 rounded-full overflow-hidden">
        <div class="h-full bg-blue-500 transition-all duration-500" style="width:${pct}%"></div>
      </div>
      <div class="mt-1 text-xs text-slate-400 flex justify-between">
        <span>进度：${pct}%</span>
        <span>耗时：${elapsed}</span>
      </div>
    </div>
  `;
}

function getCitationLabel(type = "") {
  if (type === "annual_report") return "年报证据";
  if (type === "sec_companyfacts" || type === "cn_finance") return "财务数据";
  if (type === "ranking") return "榜单数据";
  return "引用";
}

function normalizeCitationUrl(rawUrl = "") {
  const text = String(rawUrl || "").trim();
  if (!text) return "";
  try {
    const parsed = new URL(text, API_ORIGIN);
    if (parsed.pathname.startsWith("/api/source/")) {
      return `${API_ORIGIN}${parsed.pathname}${parsed.search}`;
    }
    return parsed.toString();
  } catch (_e) {
    return text;
  }
}

function buildTableHtml(table) {
  if (!table || !Array.isArray(table.columns) || !Array.isArray(table.rows)) {
    return "";
  }
  return `
    <div class="mt-3 pt-3 border-t border-slate-200">
      <div class="text-xs font-medium text-slate-600 mb-2">数据表格（自动抽取）</div>
      <div class="text-xs text-slate-500 mb-2">不会改变回答内容；仅在检测到趋势/时间序列/排名等数据时才显示。</div>
      <div class="overflow-x-auto">
        <table class="w-full text-xs border-collapse bg-white">
          <thead>
            <tr>
              ${table.columns
                .map(
                  (c) =>
                    `<th class="border border-slate-200 px-2 py-1 text-left">${esc(String(c))}</th>`
                )
                .join("")}
            </tr>
          </thead>
          <tbody>
            ${table.rows
              .map(
                (r) =>
                  `<tr>${(r || [])
                    .map(
                      (c) =>
                        `<td class="border border-slate-200 px-2 py-1">${esc(String(c))}</td>`
                    )
                    .join("")}</tr>`
              )
              .join("")}
          </tbody>
        </table>
      </div>
    </div>
  `;
}

function buildCitationsHtml(citations = []) {
  if (!citations.length) return "";
  return `
    <div class="text-xs text-slate-500">引用说明：D=年报片段证据，F=财务数据来源，R=榜单/排名数据来源。</div>
    <div class="mt-3 pt-3 border-t border-slate-200">
      <div class="text-xs font-medium text-slate-600 mb-2">引用</div>
      <ul class="text-xs text-slate-600 space-y-1">
        ${citations
          .map((c) => {
            const label = getCitationLabel(c.type || "");
            const title = esc(c.title || "");
            const safeUrl = normalizeCitationUrl(c.url || "");
            const url = safeUrl
              ? ` <a class="text-slate-900 underline" target="_blank" rel="noreferrer" href="${esc(safeUrl)}">打开</a>`
              : "";
            return `<li><span class="font-semibold">${label}</span>：${title}${url}</li>`;
          })
          .join("")}
      </ul>
    </div>
  `;
}

function buildSourceBadgeHtml(citations = [], dataSource = "") {
  if (!dataSource || dataSource === "glm_only") return "";
  const hasAkshare = citations.some((c) => String(c.title || "").toLowerCase().includes("akshare"));
  const hasEODHD = citations.some((c) => String(c.title || "").toLowerCase().includes("eodhd"));
  const hasValueCell = citations.some((c) => String(c.title || "").toLowerCase().includes("valuecell"));
  const providerHit =
    citations.find((c) => String(c.title || "").includes("查询记录")) ||
    citations.find((c) => {
    const t = String(c.title || "").toLowerCase();
    return t.includes("akshare") || t.includes("tushare") || t.includes("eodhd") || t.includes("valuecell");
  });
  if (!providerHit) return "";
  let providerName = "数据接口";
  if (hasValueCell) providerName = "ValueCell";
  else if (hasAkshare && hasEODHD) providerName = "AkShare + EODHD";
  else if (String(providerHit.title || "").toLowerCase().includes("akshare")) providerName = "AkShare";
  else if (String(providerHit.title || "").toLowerCase().includes("eodhd")) providerName = "EODHD";
  else providerName = "Tushare";
  const traceUrl = normalizeCitationUrl(providerHit.url || "");
  const traceLink = traceUrl
    ? `<a class="underline ml-2" target="_blank" rel="noreferrer" href="${esc(traceUrl)}">查看查询记录</a>`
    : "";
  return `
    <div class="mb-2 text-xs bg-emerald-50 text-emerald-700 border border-emerald-200 rounded-lg px-2 py-1 inline-block">
      数据来源：${providerName}（已调用）${traceLink}
    </div>
  `;
}

function buildDataSourceBadge(dataSource = "") {
  if (!dataSource) return "";
  if (dataSource === "professional") {
    return `<div class="mb-2 text-xs bg-emerald-50 text-emerald-700 border border-emerald-200 rounded-lg px-2 py-1 inline-block">数据通道：数据对比（AkShare + EODHD）</div>`;
  }
  if (dataSource === "valuecell") {
    return `<div class="mb-2 text-xs bg-emerald-50 text-emerald-700 border border-emerald-200 rounded-lg px-2 py-1 inline-block">数据通道：ValueCell 深度分析</div>`;
  }
  if (dataSource === "eodhd") {
    return `<div class="mb-2 text-xs bg-emerald-50 text-emerald-700 border border-emerald-200 rounded-lg px-2 py-1 inline-block">数据通道：EODHD 美股接口</div>`;
  }
  if (dataSource === "akshare") {
    return `<div class="mb-2 text-xs bg-emerald-50 text-emerald-700 border border-emerald-200 rounded-lg px-2 py-1 inline-block">数据通道：AkShare 实时查询</div>`;
  }
  if (dataSource === "tushare") {
    return `<div class="mb-2 text-xs bg-emerald-50 text-emerald-700 border border-emerald-200 rounded-lg px-2 py-1 inline-block">数据通道：Tushare 实时查询</div>`;
  }
  if (dataSource === "local_fallback") {
    return `<div class="mb-2 text-xs bg-amber-50 text-amber-700 border border-amber-200 rounded-lg px-2 py-1 inline-block">数据通道：本地回退（AkShare 未命中）</div>`;
  }
  return `<div class="mb-2 text-xs bg-slate-100 text-slate-700 border border-slate-200 rounded-lg px-2 py-1 inline-block">数据通道：仅模型回答</div>`;
}

function renderAssistantHtml(payload = {}) {
  const answer = esc(payload.answer || "").replace(/\n/g, "<br/>");
  const citations = payload.citations || [];
  const dataSource = payload.dataSource || "";
  return `${buildDataSourceBadge(dataSource)}${buildSourceBadgeHtml(citations, dataSource)}<div>${answer}</div>${buildTableHtml(payload.table)}${buildCitationsHtml(citations)}`;
}

function normalizeChartPayload(rawChart) {
  if (!rawChart || !Array.isArray(rawChart.series) || !rawChart.series.length) {
    return null;
  }

  const xSet = new Set();
  for (const series of rawChart.series) {
    for (const p of series.points || []) {
      xSet.add(p.x);
    }
  }

  let labels = Array.from(xSet);
  const allNumeric = labels.every(
    (v) => typeof v === "number" || (String(v).trim() !== "" && !Number.isNaN(Number(v)))
  );
  labels = allNumeric
    ? labels.map((v) => Number(v)).sort((a, b) => a - b)
    : labels.map((v) => String(v));

  const datasets = rawChart.series.map((series) => {
    const pointsMap = new Map(
      (series.points || []).map((p) => [allNumeric ? Number(p.x) : String(p.x), p.y])
    );
    return {
      label: series.name,
      data: labels.map((x) => (pointsMap.has(x) ? pointsMap.get(x) : null))
    };
  });

  return {
    type: rawChart.type || "line",
    labels,
    datasets,
    title: rawChart.title || "",
    xLabel: rawChart.xLabel || "年度",
    yLabel: rawChart.yLabel || "数值"
  };
}

export default function App() {
  const sessionRef = useRef(localStorage.getItem("fa_sessionId"));
  const [sessionId, setSessionId] = useState(sessionRef.current);
  const [messages, setMessages] = useState([{ id: newId(), role: "assistant", html: WELCOME_HTML }]);
  const [input, setInput] = useState("");
  const [isSending, setIsSending] = useState(false);
  const [analysisMode, setAnalysisMode] = useState("normal");
  const [chartPayload, setChartPayload] = useState(null);
  const [chartNote, setChartNote] = useState("暂无图表");
  const [hotspots, setHotspots] = useState([]);
  const [hotspotsLoading, setHotspotsLoading] = useState(false);
  const [hotspotsError, setHotspotsError] = useState("");
  const [hotspotsUpdatedAt, setHotspotsUpdatedAt] = useState("");
  const [hotspotsTraceUrl, setHotspotsTraceUrl] = useState("");

  const messagesRef = useRef(null);
  const chartCanvasRef = useRef(null);
  const chartRef = useRef(null);

  useEffect(() => {
    if (messagesRef.current) {
      messagesRef.current.scrollTop = messagesRef.current.scrollHeight;
    }
  }, [messages]);

  useEffect(() => {
    const normalized = normalizeChartPayload(chartPayload);
    if (!normalized || !chartCanvasRef.current) {
      if (chartRef.current) {
        chartRef.current.destroy();
        chartRef.current = null;
      }
      setChartNote("暂无图表");
      return;
    }

    const ctx = chartCanvasRef.current.getContext("2d");
    if (!ctx) return;

    if (chartRef.current) {
      chartRef.current.destroy();
    }

    chartRef.current = new Chart(ctx, {
      type: normalized.type,
      data: {
        labels: normalized.labels,
        datasets: normalized.datasets
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        plugins: {
          title: {
            display: Boolean(normalized.title),
            text: normalized.title
          }
        },
        scales: {
          x: {
            ticks: {
              autoSkip: true,
              maxRotation: 0
            },
            title: {
              display: true,
              text: normalized.xLabel
            }
          },
          y: {
            title: {
              display: true,
              text: normalized.yLabel
            }
          }
        }
      }
    });
    setChartNote(normalized.title || "图表已更新");

    return () => {
      if (chartRef.current) {
        chartRef.current.destroy();
        chartRef.current = null;
      }
    };
  }, [chartPayload]);

  const loadHotspots = async () => {
    setHotspotsLoading(true);
    setHotspotsError("");
    try {
      const r = await fetch(`${API_BASE}/market/hotspots?limit=8`);
      const data = await r.json();
      if (!r.ok) {
        throw new Error(data.error || "市场热点拉取失败");
      }
      const items = Array.isArray(data.items) ? data.items : [];
      setHotspots(items);
      setHotspotsUpdatedAt(data.updatedAt || "");
      setHotspotsTraceUrl(normalizeCitationUrl(data.traceUrl || ""));
    } catch (err) {
      setHotspots([]);
      setHotspotsError(err?.message || "市场热点拉取失败");
    } finally {
      setHotspotsLoading(false);
    }
  };

  useEffect(() => {
    if (analysisMode === "deep") {
      void loadHotspots();
    }
  }, [analysisMode]);

  const updateSession = (next) => {
    sessionRef.current = next;
    setSessionId(next);
  };

  const ensureSession = async () => {
    if (sessionRef.current) return sessionRef.current;

    const r = await fetch(`${API_BASE}/session`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({})
    });
    const data = await r.json();
    if (!r.ok || !data.sessionId) {
      throw new Error(data.error || "创建会话失败");
    }

    localStorage.setItem("fa_sessionId", data.sessionId);
    updateSession(data.sessionId);
    return data.sessionId;
  };

  const pushMessage = (role, html, id = newId()) => {
    setMessages((prev) => [...prev, { id, role, html }]);
    return id;
  };

  const removeMessage = (id) => {
    setMessages((prev) => prev.filter((m) => m.id !== id));
  };

  const updateMessage = (id, html) => {
    setMessages((prev) => prev.map((m) => (m.id === id ? { ...m, html } : m)));
  };

  const send = async (text) => {
    const message = text.trim();
    if (!message || isSending) return;
    let requestMessage = message;
    if (analysisMode === "compare") {
      requestMessage = `#mode:pro ${message}`;
    } else if (analysisMode === "deep") {
      requestMessage = `#mode:deep ${message}`;
    }
    const startedAt = Date.now();

    setInput("");
    pushMessage("user", esc(message));
    const loadingId = pushMessage(
      "assistant",
      buildProgressHtml({ detail: "正在准备请求...", stage: "received" }, 0)
    );
    setIsSending(true);

    let sid = "";
    let pollingStopped = false;
    let pollingTimer = null;
    let elapsedTimer = null;
    let latestStatus = { detail: "正在准备请求...", stage: "received" };
    const renderProgress = () => {
      if (pollingStopped) return;
      updateMessage(loadingId, buildProgressHtml(latestStatus, Date.now() - startedAt));
    };
    const stopProgressTimers = () => {
      pollingStopped = true;
      if (pollingTimer) window.clearInterval(pollingTimer);
      if (elapsedTimer) window.clearInterval(elapsedTimer);
    };
    try {
      elapsedTimer = window.setInterval(() => {
        renderProgress();
      }, 250);
      sid = await ensureSession();
      const pollStatus = async () => {
        try {
          const sr = await fetch(`${API_BASE}/chat/status/${sid}`);
          if (!sr.ok) return;
          const status = await sr.json();
          if (status && typeof status === "object") {
            latestStatus = status;
          }
          renderProgress();
        } catch (_e) {
          // ignore polling errors
        }
      };
      await pollStatus();
      pollingTimer = window.setInterval(() => {
        if (!pollingStopped) {
          void pollStatus();
        }
      }, 800);

      const r = await fetch(`${API_BASE}/chat`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ sessionId: sid, message: requestMessage })
      });
      const data = await r.json();
      stopProgressTimers();
      removeMessage(loadingId);

      if (!r.ok) {
        pushMessage("assistant", `<span class="text-red-600">${esc(data.error || "请求失败")}</span>`);
        return;
      }

      pushMessage("assistant", renderAssistantHtml(data));
      setChartPayload(data.chart || null);
    } catch (err) {
      stopProgressTimers();
      removeMessage(loadingId);
      pushMessage("assistant", `<span class="text-red-600">${esc(err.message || "请求失败")}</span>`);
    } finally {
      stopProgressTimers();
      setIsSending(false);
    }
  };

  const handleNewConversation = async () => {
    localStorage.removeItem("fa_sessionId");
    updateSession(null);
    setMessages([{ id: newId(), role: "assistant", html: "已开始新对话。" }]);
    setChartPayload(null);
    try {
      await ensureSession();
    } catch (err) {
      pushMessage("assistant", `<span class="text-red-600">${esc(err.message || "创建会话失败")}</span>`);
    }
  };

  const openHistory = async () => {
    const sid = await ensureSession();
    window.open(`${API_BASE}/history/${sid}`, "_blank");
  };

  const openReport = async () => {
    const sid = await ensureSession();
    window.open(`${API_BASE}/report/${sid}`, "_blank");
  };

  return (
    <div className="max-w-[1240px] mx-auto p-4 md:p-6">
      <div className="flex items-center justify-between mb-4 gap-3 flex-wrap">
        <h1 className="text-xl font-semibold">财务分析智能助手（Quentin）</h1>
        <div className="flex gap-2 flex-wrap">
          <button
            id="btnNew"
            className="px-3 py-2 rounded-xl bg-white border border-slate-200 hover:bg-slate-100"
            onClick={handleNewConversation}
            type="button"
          >
            新对话
          </button>
          <button
            id="btnHistory"
            className="px-3 py-2 rounded-xl bg-white border border-slate-200 hover:bg-slate-100"
            onClick={openHistory}
            type="button"
          >
            查看历史
          </button>
          <button
            id="btnExport"
            className="px-3 py-2 rounded-xl bg-slate-900 text-white hover:bg-slate-800"
            onClick={openReport}
            type="button"
          >
            导出报告
          </button>
        </div>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-5 gap-4">
        <div className="md:col-span-3 bg-white border border-slate-200 rounded-2xl p-4 shadow-sm">
          <div
            id="messages"
            className="space-y-3 h-[520px] overflow-auto pr-2 overflow-x-hidden"
            ref={messagesRef}
          >
            {messages.map((m) => (
              <div
                key={m.id}
                className={`msg ${
                  m.role === "user"
                    ? "bg-slate-900 text-white rounded-2xl p-3 ml-10"
                    : "bg-slate-50 border border-slate-200 rounded-2xl p-3 mr-10"
                }`}
                dangerouslySetInnerHTML={{ __html: m.html }}
              />
            ))}
          </div>

          <div className="mt-4 flex flex-wrap items-center gap-2 text-xs">
            <button
              className={`px-3 py-2 rounded-lg border transition-colors ${
                analysisMode === "compare"
                  ? "bg-blue-600 border-blue-600 text-white hover:bg-blue-500"
                  : "bg-slate-100 border-slate-300 text-slate-700 hover:bg-slate-200"
              }`}
              type="button"
              aria-pressed={analysisMode === "compare"}
              onClick={() => setAnalysisMode((v) => (v === "compare" ? "normal" : "compare"))}
            >
              数据对比
            </button>
            <button
              className={`px-3 py-2 rounded-lg border transition-colors ${
                analysisMode === "deep"
                  ? "bg-blue-600 border-blue-600 text-white hover:bg-blue-500"
                  : "bg-slate-100 border-slate-300 text-slate-700 hover:bg-slate-200"
              }`}
              type="button"
              aria-pressed={analysisMode === "deep"}
              onClick={() => setAnalysisMode((v) => (v === "deep" ? "normal" : "deep"))}
            >
              深度分析
            </button>
          </div>

          <form
            className="mt-3 flex gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              send(input);
            }}
          >
            <input
              className="flex-1 rounded-xl border border-slate-200 px-4 py-3 min-w-0 w-full text-slate-800 placeholder:text-slate-400 focus:outline-none focus:ring-2 focus:ring-blue-100 focus:border-blue-300"
              placeholder="输入问题，例如：TSLA 最新10-K 风险因素要点？"
              value={input}
              onChange={(e) => setInput(e.target.value)}
            />
            <button
              className="px-4 py-3 rounded-xl bg-slate-900 text-white hover:bg-slate-800 disabled:opacity-70"
              disabled={isSending}
              type="submit"
            >
              发送
            </button>
          </form>

          <div className="text-xs text-slate-600 leading-relaxed mt-3">
            API Key 全部由服务端 <span className="mono">server/.env</span> 管理；如需启用年报 embedding
            重排，请在 <span className="mono">server/.env</span> 配置 <span className="mono">GOOGLE_API_KEY</span>。
          </div>

          <div className="text-xs text-slate-500 mt-2">Session: {sessionId || "未创建"}</div>
        </div>

        {analysisMode === "deep" ? (
          <div className="md:col-span-2 bg-white border border-slate-200 rounded-2xl p-4 shadow-sm">
            <div className="flex items-start justify-between gap-3 pb-3 mb-3 border-b border-slate-100">
              <div className="font-semibold text-slate-900 text-lg leading-none whitespace-nowrap">🔥 市场热点</div>
              <div className="flex items-center gap-2 text-[11px] text-slate-500 whitespace-nowrap">
                {hotspotsUpdatedAt ? (
                  <span className="px-1.5 py-0.5 rounded bg-slate-100 text-slate-600">
                    更新于 {formatHotspotUpdatedAt(hotspotsUpdatedAt)}
                  </span>
                ) : null}
                {hotspotsTraceUrl ? (
                  <a
                    className="underline text-slate-600 hover:text-slate-800"
                    href={hotspotsTraceUrl}
                    target="_blank"
                    rel="noreferrer"
                  >
                    查看来源
                  </a>
                ) : null}
                <button
                  type="button"
                  className="px-2 py-1 rounded border border-slate-300 bg-slate-50 text-slate-600 hover:bg-slate-100 disabled:opacity-60"
                  onClick={() => {
                    void loadHotspots();
                  }}
                  disabled={hotspotsLoading}
                >
                  {hotspotsLoading ? "刷新中..." : "刷新"}
                </button>
              </div>
            </div>
            {hotspotsError ? <div className="text-xs text-rose-600 mb-2">{hotspotsError}</div> : null}
            {!hotspotsError && hotspots.length === 0 && !hotspotsLoading ? (
              <div className="text-xs text-slate-500 mb-2">暂无热点数据</div>
            ) : null}
            <div className="hotspot-scroll h-[520px] overflow-y-auto pr-1 space-y-2">
              {hotspots.map((item, idx) => {
                const title = String(item?.title || "").trim();
                const category = String(item?.category || "市场观察").trim();
                const symbol = String(item?.symbol || "").trim();
                if (!title) return null;
                return (
                  <button
                    key={`${idx}-${title}`}
                    type="button"
                    className="w-full text-left rounded-xl border border-slate-200 bg-slate-50/60 px-3 py-2.5 hover:bg-white hover:border-slate-300 transition"
                    onClick={() => {
                      send(title);
                    }}
                    disabled={isSending}
                  >
                    <div className="flex items-start justify-between gap-2">
                      <div className="flex-1 min-w-0">
                        <span className="inline-block mr-2 px-2 py-0.5 rounded-full bg-slate-200/80 text-slate-600 text-[11px]">
                          {category}
                        </span>
                        <span className="text-sm leading-relaxed text-slate-800">{title}</span>
                      </div>
                      <div className="text-xs text-amber-600 whitespace-nowrap">{symbol || "市场热点"}</div>
                    </div>
                  </button>
                );
              })}
            </div>
          </div>
        ) : (
          <div className="md:col-span-2 bg-white border border-slate-200 rounded-2xl p-4 shadow-sm">
            <div className="font-medium mb-2">图表</div>
            <div className="h-[260px]">
              <canvas id="chart" ref={chartCanvasRef} height="260" />
            </div>
            <div id="chartNote" className="text-xs text-slate-500 mt-2">
              {chartNote}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
