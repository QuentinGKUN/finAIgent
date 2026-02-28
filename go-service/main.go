package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const glmURL = "https://open.bigmodel.cn/api/paas/v4/chat/completions"
const glmTimeout = 15 * time.Second

var glmHTTPClient = &http.Client{Timeout: glmTimeout}

type ChatMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

type FactPoint struct {
	FY    int     `json:"fy"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

type CNStockFile struct {
	Stocks []CNStock `json:"stocks"`
}

type CNStock struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Market string `json:"market"`
}

type CNFinanceCompany struct {
	Name string          `json:"name"`
	Data []CNFinanceItem `json:"data"`
}

type CNFinanceItem struct {
	Year             int      `json:"year"`
	Revenue          *float64 `json:"revenue"`
	GrossProfit      *float64 `json:"gross_profit"`
	OperatingIncome  *float64 `json:"operating_income"`
	NetIncome        *float64 `json:"net_income"`
	RAndD            *float64 `json:"rd"`
	TotalAssets      *float64 `json:"total_assets"`
	TotalLiabilities *float64 `json:"total_liabilities"`
	Unit             string   `json:"unit"`
}

type TushareResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Fields []string `json:"fields"`
		Items  []any    `json:"items"`
	} `json:"data"`
}

type BoardPerfItem struct {
	Code       string  `json:"code"`
	Name       string  `json:"name"`
	StartClose float64 `json:"start_close"`
	EndClose   float64 `json:"end_close"`
	PctChange  float64 `json:"pct_change"`
	StartDate  string  `json:"start_date"`
	EndDate    string  `json:"end_date"`
}

type StockRankItem struct {
	Code       string   `json:"code"`
	Name       string   `json:"name"`
	PctChange  float64  `json:"pct_change"`
	Latest     *float64 `json:"latest_price"`
	Turnover   *float64 `json:"turnover"`
	PeriodHint string   `json:"-"`
}

type CompareTarget struct {
	Name   string
	Symbol string
	Market string // CN or US
}

type ComparePlan struct {
	Targets   []string `json:"targets"`
	Years     []int    `json:"years"`
	Metrics   []string `json:"metrics"`
	NeedChart bool     `json:"needChart"`
	NeedTable bool     `json:"needTable"`
}

type DataDecision struct {
	NeedData bool   `json:"needData"`
	Route    string `json:"route"` // compare | finance | stock_rank | board | none
	Reason   string `json:"reason"`
}

type CompareSeriesData struct {
	Target    CompareTarget
	Series    map[string][]FactPoint
	SourceURL string
	QueryCode string
	SourceTag string
}

type ReportItem struct {
	ID         string           `json:"id"`
	Question   string           `json:"question"`
	Answer     string           `json:"answer"`
	Evidence   string           `json:"evidence"`
	DataSource string           `json:"data_source"`
	Citations  []map[string]any `json:"citations"`
	Table      any              `json:"table"`
	Chart      any              `json:"chart"`
	CreatedAt  string           `json:"created_at"`
}

type ChatProgress struct {
	Stage     string `json:"stage"`
	Detail    string `json:"detail"`
	Done      bool   `json:"done"`
	Progress  int    `json:"progress"`
	ElapsedMs int64  `json:"elapsedMs"`
	StartedAt string `json:"startedAt"`
	UpdatedAt string `json:"updatedAt"`
}

type MarketHotspot struct {
	Category string `json:"category"`
	Title    string `json:"title"`
	Symbol   string `json:"symbol"`
}

var (
	storeMu      sync.RWMutex
	sessions     = map[string][]ChatMessage{}
	reports      = map[string][]ReportItem{}
	chatProgress = map[string]ChatProgress{}
)

func main() {
	loadDotEnvIfExists("../server/.env")
	loadDotEnvIfExists(".env")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", handleHealth)
	mux.HandleFunc("/api/session", handleSession)
	mux.HandleFunc("/api/chat", handleChat)
	mux.HandleFunc("/api/chat/status/", handleChatStatus)
	mux.HandleFunc("/api/source/tushare", handleTushareSource)
	mux.HandleFunc("/api/source/akshare", handleTushareSource)
	mux.HandleFunc("/api/source/eodhd", handleEODHDSource)
	mux.HandleFunc("/api/source/valuecell", handleValueCellSource)
	mux.HandleFunc("/api/market/hotspots", handleMarketHotspots)
	mux.HandleFunc("/api/history/", handleHistory)
	mux.HandleFunc("/api/report/", handleReport)

	port := getenv("PORT", getenv("GO_PORT", "3000"))
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           withCORS(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("Unified Go backend http://localhost:%s", port)
	log.Fatal(srv.ListenAndServe())
}

func loadDotEnvIfExists(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(string(b), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pos := strings.Index(line, "=")
		if pos <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:pos])
		val := strings.Trim(strings.TrimSpace(line[pos+1:]), `"'`)
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func withCORS(next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, User-Agent")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeTraceHTML(w http.ResponseWriter, title, body string) {
	content := "<!doctype html><html><head><meta charset=\"utf-8\"/><title>" + htmlEsc(title) + "</title></head>"
	content += "<body style=\"font-family:Arial;padding:20px;\">"
	content += "<h2>" + htmlEsc(title) + "</h2>"
	content += body
	content += "</body></html>"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(content))
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "time": time.Now().UTC().Format(time.RFC3339)})
}

func handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
		return
	}
	var req struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req)
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = newID()
	}
	ensureSession(sessionID)
	writeJSON(w, 200, map[string]any{"sessionId": sessionID})
}

func handleChatStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
		return
	}
	sessionID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/chat/status/"))
	if sessionID == "" {
		writeJSON(w, 400, map[string]any{"error": "missing sessionId"})
		return
	}
	p := getChatProgress(sessionID)
	if strings.TrimSpace(p.UpdatedAt) == "" {
		p = ChatProgress{
			Stage:     "idle",
			Detail:    "空闲",
			Done:      true,
			Progress:  100,
			ElapsedMs: 0,
			StartedAt: time.Now().UTC().Format(time.RFC3339),
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		}
	}
	writeJSON(w, 200, p)
}

func ensureSession(sessionID string) {
	storeMu.Lock()
	defer storeMu.Unlock()
	if _, ok := sessions[sessionID]; !ok {
		sessions[sessionID] = []ChatMessage{}
	}
}

func appendMessage(sessionID, role, content string) {
	storeMu.Lock()
	defer storeMu.Unlock()
	sessions[sessionID] = append(sessions[sessionID], ChatMessage{
		Role:      role,
		Content:   content,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

func appendReportItem(sessionID string, item ReportItem) {
	storeMu.Lock()
	defer storeMu.Unlock()
	reports[sessionID] = append(reports[sessionID], item)
}

func getMessages(sessionID string) []ChatMessage {
	storeMu.RLock()
	defer storeMu.RUnlock()
	src := sessions[sessionID]
	out := make([]ChatMessage, len(src))
	copy(out, src)
	return out
}

func getReportItems(sessionID string) []ReportItem {
	storeMu.RLock()
	defer storeMu.RUnlock()
	src := reports[sessionID]
	out := make([]ReportItem, len(src))
	copy(out, src)
	return out
}

func setChatProgress(sessionID, stage, detail string, done bool) {
	storeMu.Lock()
	defer storeMu.Unlock()
	now := time.Now().UTC()
	prev := chatProgress[sessionID]
	startedAt := now
	if strings.TrimSpace(prev.StartedAt) != "" {
		if t, err := time.Parse(time.RFC3339, prev.StartedAt); err == nil {
			startedAt = t
		}
	}
	if strings.EqualFold(strings.TrimSpace(stage), "received") || strings.TrimSpace(prev.StartedAt) == "" {
		startedAt = now
	}
	progress := progressByStage(stage, done)
	elapsed := now.Sub(startedAt).Milliseconds()
	if elapsed < 0 {
		elapsed = 0
	}
	chatProgress[sessionID] = ChatProgress{
		Stage:     strings.TrimSpace(stage),
		Detail:    strings.TrimSpace(detail),
		Done:      done,
		Progress:  progress,
		ElapsedMs: elapsed,
		StartedAt: startedAt.Format(time.RFC3339),
		UpdatedAt: now.Format(time.RFC3339),
	}
}

func getChatProgress(sessionID string) ChatProgress {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return chatProgress[sessionID]
}

func progressByStage(stage string, done bool) int {
	if done {
		return 100
	}
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case "received":
		return 5
	case "memory":
		return 12
	case "route":
		return 20
	case "compare_plan":
		return 30
	case "compare_query":
		return 45
	case "compare_render":
		return 62
	case "stock_rank":
		return 55
	case "board":
		return 55
	case "finance_target":
		return 35
	case "finance_query":
		return 58
	case "finance_summary":
		return 72
	case "deep_plan":
		return 28
	case "deep_query":
		return 62
	case "deep_summary":
		return 86
	case "llm_summary":
		return 82
	case "llm":
		return 88
	case "done":
		return 100
	case "idle":
		return 100
	default:
		return 15
	}
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
		return
	}
	var req struct {
		SessionID string `json:"sessionId"`
		Message   string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid body"})
		return
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Message = strings.TrimSpace(req.Message)
	if req.SessionID == "" || req.Message == "" {
		writeJSON(w, 400, map[string]any{"error": "missing sessionId or message"})
		return
	}
	ensureSession(req.SessionID)
	appendMessage(req.SessionID, "user", req.Message)
	setChatProgress(req.SessionID, "received", "已接收问题，正在分析意图", false)
	progress := func(stage, detail string) {
		setChatProgress(req.SessionID, stage, detail, false)
	}
	history := getMessages(req.SessionID)

	evidence := ""
	citations := []map[string]any{}
	var chart any
	var table any
	answer := ""
	var err error
	apiKey := ""
	valueCellSucceeded := false
	deepFallbackNotice := ""
	originMessage := req.Message
	skillCompare := false
	professionalMode := false
	deepMode := false
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(req.Message)), "#mode:deep") {
		deepMode = true
		req.Message = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(req.Message), "#mode:deep"))
		if req.Message == "" {
			req.Message = originMessage
		}
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(req.Message)), "#mode:pro") {
		professionalMode = true
		req.Message = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(req.Message), "#mode:pro"))
		if req.Message == "" {
			req.Message = originMessage
		}
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(req.Message)), "#skill:compare") {
		skillCompare = true
		req.Message = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(req.Message), "#skill:compare"))
		if req.Message == "" {
			req.Message = originMessage
		}
	}
	userMessage := req.Message
	progress("memory", "正在整理历史对话与数据记忆")
	memorySummary := collectMemorySummaryV2(req.SessionID, 4, 1000)
	memoryEvidence := collectMemoryEvidenceV2(req.SessionID, 4, 1400)

	if !deepMode && shouldCarryTargetsV2(req.Message) && !hasExplicitTargets(req.Message) {
		if last := getLastTargets(req.SessionID); len(last) > 0 {
			if len(last) >= 2 {
				req.Message = "对比 " + joinTargetHint(last) + " " + req.Message
			} else {
				req.Message = joinTargetHint(last) + " " + req.Message
			}
		}
	}
	workMessage := req.Message

	progress("route", "正在判断是否需要查询专业数据")
	decision := DataDecision{NeedData: false, Route: "none"}
	if deepMode {
		decision = DataDecision{NeedData: true, Route: "deep", Reason: "deep_mode"}
	} else if skillCompare {
		decision = DataDecision{NeedData: true, Route: "compare", Reason: "skill_compare"}
	} else if professionalMode {
		heuristic := heuristicDataDecision(workMessage)
		decision = heuristic
		if apiKey == "" {
			apiKey = strings.TrimSpace(os.Getenv("GLM_KEY"))
		}
		if apiKey != "" {
			if d, de := callGLMDataDecision(apiKey, workMessage, memorySummary); de == nil {
				d = normalizeDataDecision(d)
				if !d.NeedData && heuristic.NeedData && isStrongDataIntent(workMessage) {
					decision = heuristic
				} else {
					decision = d
				}
			}
		}
	}
	if professionalMode && !deepMode && !decision.NeedData {
		last := getLastTargets(req.SessionID)
		maybeTargets := resolveCompareTargets(workMessage, nil)
		switch {
		case len(maybeTargets) >= 2:
			decision = DataDecision{NeedData: true, Route: "compare", Reason: "opportunistic_targets"}
		case shouldCarryTargetsV2(workMessage) && len(last) >= 2:
			decision = DataDecision{NeedData: true, Route: "compare", Reason: "opportunistic_memory_compare"}
		case hasExplicitTargets(workMessage):
			decision = DataDecision{NeedData: true, Route: "finance", Reason: "opportunistic_explicit_target"}
		case shouldCarryTargetsV2(workMessage) && len(last) == 1:
			decision = DataDecision{NeedData: true, Route: "finance", Reason: "opportunistic_memory_finance"}
		}
	}
	dataPipelineEnabled := skillCompare || professionalMode
	route := strings.TrimSpace(decision.Route)
	allowAllRoutes := !professionalMode || route == "" || route == "none"
	compareRouteOpen := allowAllRoutes || route == "compare"
	rankRouteOpen := allowAllRoutes || route == "stock_rank"
	boardRouteOpen := allowAllRoutes || route == "board"
	financeRouteOpen := allowAllRoutes || route == "finance"
	if skillCompare {
		compareRouteOpen = true
	}
	forceCompare := decision.Route == "compare"
	forceRank := decision.Route == "stock_rank"
	forceBoard := decision.Route == "board"
	forceFinance := decision.Route == "finance"
	fallbackToLLMOnDataFailure := professionalMode && !skillCompare
	dataFailureNotes := []string{}
	recordDataFailure := func(msg string) {
		msg = strings.TrimSpace(msg)
		if msg == "" {
			return
		}
		dataFailureNotes = append(dataFailureNotes, msg)
	}

	// -1) 深度分析模式：调用 ValueCell 深度研究，再交给 LLM 总结。
	if answer == "" && deepMode {
		progress("deep_plan", "正在规划 ValueCell 深度分析任务")
		if apiKey == "" {
			apiKey = strings.TrimSpace(os.Getenv("GLM_KEY"))
		}
		// 深度模式优先使用当前问题的规则识别，避免 LLM 在多轮上下文下误引入无关公司。
		targets := resolveCompareTargets(req.Message, nil)
		if len(targets) == 0 && apiKey != "" {
			if llmTargets, te := callGLMResolveCompareTargets(apiKey, req.Message); te == nil && len(llmTargets) > 0 {
				targets = llmTargets
			}
		}
		targets = applyMarketConstraintByQuestion(req.Message, targets)
		targets = enrichTargetNames(targets)
		if !isCompareQuestion(req.Message) && len(targets) > 1 {
			targets = targets[:1]
		}
		if len(targets) > 0 {
			setLastTargets(req.SessionID, targets)
		}

		targetNames := []string{}
		for _, t := range targets {
			if strings.TrimSpace(t.Name) != "" {
				targetNames = append(targetNames, t.Name)
			}
			if len(targetNames) >= 3 {
				break
			}
		}

		progress("deep_query", "正在调用 ValueCell 进行深度研究")
		deepText, sourceURL, deepErr := getDeepAnalysisFromValueCell(req.Message, targetNames)
		traceURL := buildValueCellTraceURL(req.Message, targetNames)
		if deepErr != nil {
			msg := "ValueCell 深度分析失败：" + strings.TrimSpace(deepErr.Error())
			recordDataFailure(msg)
			deepFallbackNotice = "深度分析链路暂不可用，已自动回退到模型回答。"
			citations = append(citations, map[string]any{
				"id":    fmt.Sprintf("F%d", len(citations)+1),
				"type":  "debug",
				"title": "深度分析失败详情",
				"url":   traceURL,
				"note":  msg,
			})
		} else {
			weakResultReason := weakValueCellResultReason(deepText)
			if weakResultReason != "" {
				msg := "ValueCell 返回结果未包含可核验数据：" + weakResultReason
				recordDataFailure(msg)
				deepFallbackNotice = "深度分析未获取到有效结构化结果，已自动回退到模型回答。"
				citations = append(citations, map[string]any{
					"id":    fmt.Sprintf("F%d", len(citations)+1),
					"type":  "debug",
					"title": "深度分析查询记录",
					"url":   traceURL,
					"note":  msg,
				})
			} else {
				evidence = strings.TrimSpace("【ValueCell 深度分析原始结果】\n" + deepText)
				valueCellSucceeded = true
				citations = append(citations, map[string]any{
					"id":    fmt.Sprintf("F%d", len(citations)+1),
					"type":  "cn_finance",
					"title": "ValueCell 深度分析查询记录",
					"url":   traceURL,
					"note":  "点击查看本次深度分析查询参数",
				})
				citations = append(citations, map[string]any{
					"id":    fmt.Sprintf("F%d", len(citations)+1),
					"type":  "cn_finance",
					"title": "ValueCell 官方仓库",
					"url":   sourceURL,
					"note":  "ValueCell 项目入口",
				})
			}
		}

		if strings.TrimSpace(evidence) != "" {
			if apiKey == "" {
				answer = buildEvidenceFallbackAnswer(mergeLLMEvidence(evidence, memorySummary, memoryEvidence))
			} else {
				progress("deep_summary", "正在基于深度分析结果生成总结")
				answer, err = callGLM(apiKey, history, userMessage, mergeLLMEvidence(evidence, memorySummary, memoryEvidence))
				if err != nil {
					answer = buildEvidenceFallbackAnswer(mergeLLMEvidence(evidence, memorySummary, memoryEvidence))
				}
			}
			if valueCellSucceeded {
				answer = "深度分析通道：ValueCell（已调用）\n\n" + strings.TrimSpace(answer)
			}
		}
	}

	// 0) 对比问题（或 Skill 对比按钮）：先规划 -> 拉数据 -> 再交给 LLM 总结。
	proModeTargets := []CompareTarget{}
	if professionalMode {
		proModeTargets = resolveCompareTargets(req.Message, nil)
	}
	if answer == "" && dataPipelineEnabled && compareRouteOpen && (skillCompare || forceCompare || isCompareQuestion(req.Message) || (professionalMode && (hasCompareHint(req.Message) || len(proModeTargets) >= 2))) {
		progress("compare_plan", "正在规划公司对比查询")
		years := extractYears(req.Message)
		if len(years) == 0 {
			nowYear := time.Now().Year()
			years = []int{nowYear - 4, nowYear - 3, nowYear - 2, nowYear - 1, nowYear}
		}

		apiKey = strings.TrimSpace(os.Getenv("GLM_KEY"))
		plan := buildHeuristicComparePlan(req.Message, years)
		llmResolvedTargets := []CompareTarget{}
		if (professionalMode || skillCompare) && apiKey != "" {
			if planned, pe := callGLMComparePlan(apiKey, req.Message, years); pe == nil {
				if len(planned.Targets) > 0 {
					plan = mergeComparePlan(plan, planned)
				}
			}
			if parsed, te := callGLMResolveCompareTargets(apiKey, req.Message); te == nil && len(parsed) > 0 {
				llmResolvedTargets = parsed
			}
		}

		heuristicTargets := resolveCompareTargets(req.Message, plan.Targets)
		targets := heuristicTargets
		if professionalMode {
			// 数据对比模式优先使用 LLM 解析结果，规则映射只做兜底补全。
			targets = mergeCompareTargetLists(llmResolvedTargets, heuristicTargets, proModeTargets)
		}
		if professionalMode && len(targets) < 2 && apiKey != "" && len(llmResolvedTargets) == 0 {
			if llmTargets, te := callGLMResolveCompareTargets(apiKey, req.Message); te == nil && len(llmTargets) >= 2 {
				targets = mergeCompareTargetLists(llmTargets, targets)
			}
		}
		targets = applyMarketConstraintByQuestion(req.Message, targets)
		targets = enrichTargetNames(targets)
		if len(targets) >= 2 {
			if len(targets) > 3 {
				targets = targets[:3]
			}
			results := []CompareSeriesData{}
			errList := []string{}
			usedAkshare := false
			usedEODHD := false
			for _, t := range targets {
				progress("compare_query", "正在查询对比数据："+t.Name+"("+t.Symbol+")")
				var series map[string][]FactPoint
				var sourceURL string
				var e error
				queryCode := t.Symbol
				sourceTag := "akshare"

				if t.Market == "US" && professionalMode {
					sourceTag = "eodhd"
					series, sourceURL, e = getUSFinanceFromEODHD(t.Symbol, plan.Years)
					if e == nil && len(series) > 0 {
						usedEODHD = true
					} else {
						eodhdErr := e
						if fbCode, ok := selectAkshareFallbackCode(t); ok {
							queryCode = fbCode
							series, sourceURL, e = getCNFinanceFromAkshare(fbCode, plan.Years)
							if e == nil && len(series) > 0 {
								sourceTag = "akshare"
								usedAkshare = true
							} else {
								e = fmt.Errorf(
									"EODHD失败(%s)；AkShare回退失败(%s)",
									sanitizeEODHDError(fmt.Sprintf("%v", eodhdErr)),
									sanitizeAkshareError(fmt.Sprintf("%v", e)),
								)
							}
						} else {
							e = fmt.Errorf("%s", sanitizeEODHDError(fmt.Sprintf("%v", eodhdErr)))
						}
					}
				} else {
					series, sourceURL, e = getCNFinanceFromAkshare(t.Symbol, plan.Years)
					if e == nil && len(series) > 0 {
						usedAkshare = true
					}
				}

				if e != nil || len(series) == 0 {
					errList = append(errList, fmt.Sprintf("%s(%s): %s", t.Name, t.Symbol, sanitizePipelineError(e)))
					continue
				}
				results = append(results, CompareSeriesData{
					Target:    t,
					Series:    series,
					SourceURL: sourceURL,
					QueryCode: queryCode,
					SourceTag: sourceTag,
				})
				citeTitle := "AkShare API（查询记录） - " + t.Name + "(" + queryCode + ")"
				citeURL := buildAkshareTraceURL(queryCode, plan.Years)
				if sourceTag == "eodhd" || strings.Contains(strings.ToLower(sourceURL), "eodhd") {
					citeTitle = "EODHD 美股接口（查询记录） - " + t.Name + "(" + queryCode + ")"
					citeURL = buildEODHDTraceURL(queryCode, plan.Years)
					usedEODHD = true
				} else {
					usedAkshare = true
				}
				note := "点击可查看本次查询参数与返回数据"
				if queryCode != t.Symbol {
					note = "点击可查看本次查询参数与返回数据（回退查询代码：" + queryCode + "）"
				}
				citations = append(citations, map[string]any{
					"id":    fmt.Sprintf("F%d", len(citations)+1),
					"type":  "cn_finance",
					"title": citeTitle,
					"url":   citeURL,
					"note":  note,
				})
			}
			if usedAkshare || !usedEODHD {
				citations = append(citations, map[string]any{
					"id":    fmt.Sprintf("F%d", len(citations)+1),
					"type":  "cn_finance",
					"title": "AkShare 官方文档",
					"url":   "https://akshare.akfamily.xyz/data/stock/stock.html",
					"note":  "接口文档",
				})
			}
			if usedEODHD {
				citations = append(citations, map[string]any{
					"id":    fmt.Sprintf("F%d", len(citations)+1),
					"type":  "cn_finance",
					"title": "EODHD 官方文档",
					"url":   "https://eodhd.com/financial-apis/",
					"note":  "美股 fundamentals 接口文档",
				})
			}

			if len(results) >= 2 {
				progress("compare_render", "已获取对比数据，正在生成表格与图表")
				setLastTargets(req.SessionID, targetsFromResults(results))
				table = buildCompareTable(results, plan.Metrics)
				if shouldShowCompareChart(req.Message, plan, results) {
					chart = buildCompareChart(results, plan.Metrics)
				}
				evidence = buildCompareEvidence(results, plan.Years, plan.Metrics)

				if apiKey == "" {
					answer = buildCompareFallbackAnswer(results, plan.Years, plan.Metrics, "GLM_KEY 未配置，使用结构化结果")
				} else {
					progress("llm_summary", "正在基于查询结果生成总结")
					answer, err = callGLMCompareSummary(apiKey, history, userMessage, mergeLLMEvidence(evidence, memorySummary, memoryEvidence))
					if err != nil {
						answer = buildCompareFallbackAnswer(results, plan.Years, plan.Metrics, err.Error())
					}
				}
				if len(errList) > 0 {
					answer += "\n\n部分标的未成功获取：\n- " + strings.Join(errList, "\n- ")
				}
			} else {
				msg := "已识别为对比问题，但可用于对比的数据不足（至少需要两个标的）。\n" +
					"请明确提供两个可查询标的，例如：`对比 002594.SZ 和 TSLA 近5年营收与净利润`。"
				if len(errList) > 0 {
					msg += "\n\n当前失败详情：\n- " + strings.Join(errList, "\n- ")
				}
				if fallbackToLLMOnDataFailure {
					recordDataFailure(msg)
				} else {
					answer = msg
				}
			}
		} else {
			msg := "已识别为对比问题，但未能解析出两个标的。\n请改为：`对比 002594.SZ 和 TSLA 近5年财报` 或 `对比 比亚迪 和 特斯拉 近5年财报`。"
			if fallbackToLLMOnDataFailure {
				recordDataFailure(msg)
			} else {
				answer = msg
			}
		}
	}

	// 1) A股个股排行问题：仅在数据对比模式调用数据通道。
	if answer == "" && dataPipelineEnabled && rankRouteOpen && (forceRank || isStockRankQuestion(req.Message)) {
		progress("stock_rank", "正在查询A股个股排行数据")
		limit := extractTopLimit(req.Message, 10, 50)
		windowDays := extractWindowDays(req.Message)
		items, sourceURL, periodLabel, e := getStockRankFromAkshare(limit, windowDays)
		traceURL := buildStockRankTraceURL(limit, windowDays)
		if e == nil && len(items) > 0 {
			evidence = buildStockRankEvidenceText(items, periodLabel)
			citations = append(citations, map[string]any{
				"id":    "F1",
				"type":  "cn_finance",
				"title": "AkShare 个股排行查询记录",
				"url":   traceURL,
				"note":  "点击查看本次个股排行查询参数与原始结果",
			})
			citations = append(citations, map[string]any{
				"id":    "F2",
				"type":  "cn_finance",
				"title": "AkShare 官方文档",
				"url":   sourceURL,
				"note":  "数据接口文档",
			})
			chart = buildStockRankChart(items, periodLabel)
			table = buildStockRankTable(items, periodLabel)
			answer = buildStockRankAnswer(items, periodLabel)
		} else {
			citations = append(citations, map[string]any{
				"id":    "F1",
				"type":  "cn_finance",
				"title": "AkShare 个股排行查询记录",
				"url":   traceURL,
				"note":  "查询失败时可用于排查参数",
			})
			citations = append(citations, map[string]any{
				"id":    "F2",
				"type":  "cn_finance",
				"title": "AkShare 官方文档",
				"url":   "https://akshare.akfamily.xyz/data/stock/stock.html",
				"note":  "数据接口文档",
			})
			msg := fmt.Sprintf(
				"已识别为A股个股排行问题，但AkShare查询失败：%v。\n请检查：1) Python 已安装 akshare/pandas 2) 网络可访问 AkShare 数据源站点。",
				sanitizeAkshareError(fmt.Sprintf("%v", e)),
			)
			if fallbackToLLMOnDataFailure {
				recordDataFailure(msg)
			} else {
				answer = msg
			}
		}
	}

	// 2) A股板块涨跌类问题：仅在数据对比模式调用数据通道。
	if answer == "" && dataPipelineEnabled && boardRouteOpen && (forceBoard || isBoardAnalysisQuestion(req.Message)) {
		progress("board", "正在查询A股板块涨跌数据")
		year, month := extractYearMonth(req.Message)
		items, sourceURL, e := getBoardPerformanceFromAkshare(year, month)
		if e == nil && len(items) > 0 {
			evidence = buildBoardEvidenceText(items, year, month)
			traceURL := buildBoardTraceURL(year, month)
			citations = append(citations, map[string]any{
				"id":    "F1",
				"type":  "cn_finance",
				"title": "AkShare 板块查询记录",
				"url":   traceURL,
				"note":  "点击查看本次板块查询参数与原始结果",
			})
			citations = append(citations, map[string]any{
				"id":    "F2",
				"type":  "cn_finance",
				"title": "AkShare 官方文档",
				"url":   sourceURL,
				"note":  "数据接口文档",
			})
			chart = buildBoardChart(items, year, month)
			table = buildBoardTable(items, year, month)
			answer = buildBoardAnswer(items, year, month)
		} else if e != nil {
			citations = append(citations, map[string]any{
				"id":    "F1",
				"type":  "cn_finance",
				"title": "AkShare 板块查询记录",
				"url":   buildBoardTraceURL(year, month),
				"note":  "查询失败时可用于排查参数",
			})
			citations = append(citations, map[string]any{
				"id":    "F2",
				"type":  "cn_finance",
				"title": "AkShare 官方文档",
				"url":   sourceURL,
				"note":  "数据接口文档",
			})
			msg := fmt.Sprintf(
				"已识别为A股板块分析问题，但AkShare查询失败：%s。\n请检查：1) Python 已安装 akshare/pandas 2) 网络可访问 AkShare 数据源站点 3) 查询月份是否有交易数据。",
				sanitizeAkshareError(e.Error()),
			)
			if fallbackToLLMOnDataFailure {
				recordDataFailure(msg)
			} else {
				answer = msg
			}
		}
	}

	// 3) 公司财务问题：仅在数据对比模式调用数据通道。
	if answer == "" && dataPipelineEnabled && financeRouteOpen && (forceFinance || isFinancialQuestion(req.Message)) {
		progress("finance_target", "正在识别需要查询的公司标的")
		years := extractYears(req.Message)
		target := CompareTarget{}
		ok := false
		if professionalMode && (hasExplicitTargets(req.Message) || len(getLastTargets(req.SessionID)) > 0 || forceFinance) {
			if apiKey == "" {
				apiKey = strings.TrimSpace(os.Getenv("GLM_KEY"))
			}
			if apiKey != "" {
				if llmTargets, te := callGLMResolveCompareTargets(apiKey, req.Message); te == nil && len(llmTargets) > 0 {
					target = llmTargets[0]
					ok = true
				}
			}
		}
		if !ok {
			target, ok = resolveFinancialTarget(req.Message)
		}
		if !ok && !hasExplicitTargets(req.Message) {
			if last := getLastTargets(req.SessionID); len(last) > 0 {
				target = last[0]
				ok = true
			}
		}
		if ok {
			target = enrichTargetName(target)
			progress("finance_query", "正在查询财务数据："+target.Name+"("+target.Symbol+")")
			setLastTargets(req.SessionID, []CompareTarget{target})
			series := map[string][]FactPoint(nil)
			sourceURL := ""
			aqErr := error(nil)
			sourceTag := "akshare"
			queryCode := target.Symbol
			if professionalMode && target.Market == "US" {
				usSeries, usSource, usErr := getUSFinanceFromEODHD(target.Symbol, years)
				if usErr == nil && len(usSeries) > 0 {
					series = usSeries
					sourceURL = usSource
					aqErr = nil
					sourceTag = "eodhd"
				} else {
					sourceTag = "eodhd"
					if fbCode, ok := selectAkshareFallbackCode(target); ok {
						queryCode = fbCode
						fbSeries, fbSource, fbErr := getCNFinanceFromAkshare(fbCode, years)
						if fbErr == nil && len(fbSeries) > 0 {
							series = fbSeries
							sourceURL = fbSource
							aqErr = nil
							sourceTag = "akshare"
						} else {
							aqErr = fmt.Errorf(
								"EODHD失败(%s)；AkShare回退失败(%s)",
								sanitizeEODHDError(fmt.Sprintf("%v", usErr)),
								sanitizeAkshareError(fmt.Sprintf("%v", fbErr)),
							)
						}
					} else {
						aqErr = fmt.Errorf("%s", sanitizeEODHDError(fmt.Sprintf("%v", usErr)))
					}
				}
			} else {
				series, sourceURL, aqErr = getCNFinanceFromAkshare(target.Symbol, years)
			}

			// 非海外标的失败时回退本地数据；海外标的优先给出 EODHD 错误，避免错误回退到本地A股数据。
			if aqErr != nil || len(series) == 0 {
				if sourceTag == "eodhd" {
					msg := fmt.Sprintf(
						"已识别到海外财务问题，但EODHD查询失败：%v。\n请检查 EODHD_API_KEY 是否有效，或改用可查询的美股/港股代码（如 BABA、0700.HK）。",
						sanitizePipelineError(aqErr),
					)
					if fallbackToLLMOnDataFailure {
						recordDataFailure(msg)
					} else {
						answer = msg
					}
				} else {
					localSeries, localURL, localErr := getCNFinanceFromLocal(target.Symbol, years)
					if localErr == nil && len(localSeries) > 0 {
						series = localSeries
						sourceURL = localURL
						sourceTag = "local_fallback"
					} else {
						msg := fmt.Sprintf(
							"已识别到财务问题，但数据查询失败。\nAkShare错误：%v\n本地回退错误：%v\n请检查 AkShare 依赖、网络连通性或提问中股票代码是否正确。",
							aqErr,
							localErr,
						)
						if fallbackToLLMOnDataFailure {
							recordDataFailure(msg)
						} else {
							answer = msg
						}
					}
				}
			}

			if answer == "" && len(series) > 0 {
				evidence = fmt.Sprintf("【财务数据（%s）公司：%s(%s)】\n%s", sourceTag, target.Name, target.Symbol, seriesToText(series))
				if queryCode != target.Symbol {
					evidence += "\n回退查询代码：" + queryCode
				}
				if sourceTag == "akshare" {
					traceURL := buildAkshareTraceURL(queryCode, years)
					note := "点击可查看本次查询参数与返回数据"
					if queryCode != target.Symbol {
						note = "点击可查看本次查询参数与返回数据（回退查询代码：" + queryCode + "）"
					}
					citations = append(citations, map[string]any{
						"id":    "F1",
						"type":  "cn_finance",
						"title": "AkShare API（查询记录）",
						"url":   traceURL,
						"note":  note,
					})
					citations = append(citations, map[string]any{
						"id":    "F2",
						"type":  "cn_finance",
						"title": "AkShare 官方文档",
						"url":   sourceURL,
						"note":  "接口文档",
					})
				} else if sourceTag == "eodhd" {
					traceURL := buildEODHDTraceURL(queryCode, years)
					citations = append(citations, map[string]any{
						"id":    "F1",
						"type":  "cn_finance",
						"title": "EODHD 美股接口（查询记录）",
						"url":   traceURL,
						"note":  "点击可查看本次查询参数与返回数据",
					})
					citations = append(citations, map[string]any{
						"id":    "F2",
						"type":  "cn_finance",
						"title": "EODHD 官方文档",
						"url":   sourceURL,
						"note":  "美股 fundamentals 接口文档",
					})
				} else {
					citations = append(citations, map[string]any{
						"id":    "F1",
						"type":  "cn_finance",
						"title": "本地财务数据回退",
						"url":   sourceURL,
						"note":  "AkShare 未成功时自动回退",
					})
				}
				chart = buildBestChartFromSeries(series, target.Name)
				table = buildTableFromSeries(target.Name, target.Symbol, series)
				if apiKey == "" {
					apiKey = strings.TrimSpace(os.Getenv("GLM_KEY"))
				}
				progress("finance_summary", "已获取财务数据，正在整理分析结论")
				brief := maybeCompanyBrief(apiKey, target, userMessage)
				answer = buildFinanceAnswer(target.Name, target.Symbol, sourceTag, series, brief)
			}
		}
	}

	if len(dataFailureNotes) > 0 {
		failText := "【本轮数据查询失败说明】\n- " + strings.Join(dataFailureNotes, "\n- ")
		if !deepMode || strings.TrimSpace(evidence) != "" {
			if strings.TrimSpace(evidence) == "" {
				evidence = failText
			} else {
				evidence = strings.TrimSpace(evidence) + "\n\n" + failText
			}
		}
	}

	if deepMode && strings.TrimSpace(answer) == "" && len(dataFailureNotes) > 0 && strings.TrimSpace(deepFallbackNotice) == "" {
		deepFallbackNotice = "深度分析链路暂不可用，已自动回退到模型回答。"
	}

	// 5) 其他问题或未命中数据时，才交给 GLM。
	llmEvidence := mergeLLMEvidence(evidence, memorySummary, memoryEvidence)
	if answer == "" {
		progress("llm", "正在调用模型生成回答")
		apiKey = strings.TrimSpace(os.Getenv("GLM_KEY"))
		if apiKey == "" {
			if strings.TrimSpace(llmEvidence) != "" {
				answer = buildEvidenceFallbackAnswer(llmEvidence)
			} else {
				answer = "模型服务不可用（未配置 GLM_KEY）。\n建议开启“数据对比”并使用结构化数据问题（财务对比、个股/板块排行）。"
			}
		}
	}
	if answer == "" {
		progress("llm", "模型正在组织最终回答")
		answer, err = callGLM(apiKey, history, userMessage, llmEvidence)
		if err != nil {
			if strings.TrimSpace(llmEvidence) != "" {
				answer = buildEvidenceFallbackAnswer(llmEvidence)
			} else {
				answer = "模型服务暂时不可用，已避免中断请求。\n请稍后重试，或开启“数据对比”使用 AkShare/EODHD 数据查询。"
			}
		}
	}

	answer = cleanAssistantAnswer(answer, chart != nil, table != nil)
	if deepMode && strings.TrimSpace(deepFallbackNotice) != "" {
		answer = deepFallbackNotice + "\n\n" + strings.TrimSpace(answer)
	}
	if strings.TrimSpace(answer) == "" {
		if strings.TrimSpace(evidence) != "" {
			answer = "已基于查询数据生成结论，图表和表格可直接查看右侧结果。"
		} else {
			answer = "已处理完成。"
		}
	}
	appendMessage(req.SessionID, "assistant", answer)
	dataSource := "glm_only"
	hasAkshare := false
	hasEODHD := false
	for _, c := range citations {
		title := strings.ToLower(asString(c["title"]))
		if strings.Contains(title, "eodhd") {
			hasEODHD = true
		}
		if strings.Contains(title, "akshare") {
			hasAkshare = true
		}
		if strings.Contains(title, "tushare") {
			dataSource = "tushare"
			break
		}
		if strings.Contains(title, "本地") || strings.Contains(title, "local") {
			dataSource = "local_fallback"
		}
	}
	if valueCellSucceeded {
		dataSource = "valuecell"
	} else if hasAkshare && hasEODHD {
		dataSource = "professional"
	} else if hasEODHD {
		dataSource = "eodhd"
	} else if hasAkshare && dataSource == "glm_only" {
		dataSource = "akshare"
	}
	appendReportItem(req.SessionID, ReportItem{
		ID:         newID(),
		Question:   userMessage,
		Answer:     answer,
		Evidence:   evidence,
		DataSource: dataSource,
		Citations:  citations,
		Table:      table,
		Chart:      chart,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	})
	setChatProgress(req.SessionID, "done", "回答已完成", true)
	writeJSON(w, 200, map[string]any{
		"answer":     answer,
		"citations":  citations,
		"chart":      chart,
		"table":      table,
		"dataSource": dataSource,
	})
}

func callGLM(apiKey string, history []ChatMessage, userMessage, evidence string) (string, error) {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	messages := []msg{
		{Role: "system", Content: "你是中文财务助手。简洁准确作答；无法确定时明确说明。若提供了财务证据，必须优先基于证据回答并明确来源。不得引入证据与历史摘要中未出现的新公司名称。禁止输出 Markdown 代码块、ASCII 字符画、文本表格（如 |---|），图表由系统单独展示。"},
	}
	start := 0
	if len(history) > 8 {
		start = len(history) - 8
	}
	for _, h := range history[start:] {
		role := "user"
		if h.Role == "assistant" {
			role = "assistant"
		}
		messages = append(messages, msg{Role: role, Content: h.Content})
	}
	prompt := userMessage
	if strings.TrimSpace(evidence) != "" {
		prompt = userMessage + "\n\n可用证据：\n" + evidence
	}
	messages = append(messages, msg{Role: "user", Content: prompt})

	payload, _ := json.Marshal(map[string]any{
		"model":       getenv("LLM_MODEL", "glm-4-flash"),
		"messages":    messages,
		"temperature": 0.55,
		"max_tokens":  1500,
	})
	apiURL := strings.TrimSpace(os.Getenv("GLM_API_URL"))
	if apiURL == "" {
		apiURL = glmURL
	}
	req, _ := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := glmHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("glm status=%d body=%s", resp.StatusCode, string(body))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("glm empty response")
	}
	return out.Choices[0].Message.Content, nil
}

func callGLMRaw(apiKey, systemPrompt, userPrompt string, temperature float64, maxTokens int) (string, error) {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	payload, _ := json.Marshal(map[string]any{
		"model": getenv("LLM_MODEL", "glm-4-flash"),
		"messages": []msg{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		"temperature": temperature,
		"max_tokens":  maxTokens,
	})
	apiURL := strings.TrimSpace(os.Getenv("GLM_API_URL"))
	if apiURL == "" {
		apiURL = glmURL
	}
	req, _ := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := glmHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("glm status=%d body=%s", resp.StatusCode, string(body))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("glm empty response")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

func callGLMCompareSummary(apiKey string, history []ChatMessage, userMessage, evidence string) (string, error) {
	prompt := userMessage + "\n\n请输出：\n1) 两家公司核心指标差异（营收、净利润、营业利润）\n2) 增长趋势判断\n3) 风险提示（含口径/币种差异）\n4) 明确给出结论。\n只基于提供证据。\n禁止使用代码块、ASCII 图、文本表格。"
	return callGLM(apiKey, history, prompt, evidence)
}

func callGLMDataDecision(apiKey, message, memorySummary string) (DataDecision, error) {
	system := "你是金融问答路由器。只做是否需要实时数据查询的决策，并输出 JSON。"
	user := "输出 JSON：\n" +
		`{"needData":false,"route":"none","reason":"一句话原因"}` +
		"\n规则：" +
		"\n1) route 仅允许 compare/finance/stock_rank/board/none；" +
		"\n2) 如果问题主要是解释、追问、总结、业务介绍，且历史上下文已提供可用数据，则 needData=false；" +
		"\n3) 只有明确要求新增数据口径（如最新财务、排名、板块涨跌、对比新公司）时 needData=true；" +
		"\n4) 严禁凭空引入用户未提及的新公司。" +
		"\n\n历史上下文摘要：\n" + memorySummary +
		"\n\n当前问题：\n" + message
	raw, err := callGLMRaw(apiKey, system, user, 0.1, 280)
	if err != nil {
		return DataDecision{}, err
	}
	block := extractJSONBlock(raw)
	if block == "" {
		return DataDecision{}, fmt.Errorf("data decision non-json response")
	}
	var d DataDecision
	if err := json.Unmarshal([]byte(block), &d); err != nil {
		return DataDecision{}, err
	}
	return normalizeDataDecision(d), nil
}

func normalizeDataDecision(d DataDecision) DataDecision {
	d.Route = strings.ToLower(strings.TrimSpace(d.Route))
	switch d.Route {
	case "compare", "finance", "stock_rank", "board", "none":
	default:
		d.Route = "none"
	}
	if !d.NeedData {
		d.Route = "none"
	}
	return d
}

func heuristicDataDecision(message string) DataDecision {
	switch {
	case isStockRankQuestion(message):
		return DataDecision{NeedData: true, Route: "stock_rank", Reason: "stock_rank_rule"}
	case isBoardAnalysisQuestion(message):
		return DataDecision{NeedData: true, Route: "board", Reason: "board_rule"}
	case isCompareQuestion(message) || hasCompareHint(message):
		return DataDecision{NeedData: true, Route: "compare", Reason: "compare_rule"}
	case isFinancialQuestion(message):
		return DataDecision{NeedData: true, Route: "finance", Reason: "finance_rule"}
	default:
		return DataDecision{NeedData: false, Route: "none", Reason: "fallback_none"}
	}
}

func isStrongDataIntent(message string) bool {
	if isStockRankQuestion(message) || isBoardAnalysisQuestion(message) || isCompareQuestion(message) || isFinancialQuestion(message) {
		return true
	}
	return regexp.MustCompile(`(?i)查询|拉取|获取|最新|近[0-9一二三四五六七八九十]+年|营收|净利润|财报|年报|季报|同比|环比|图表|排行|榜单|板块|涨跌|对比`).MatchString(message)
}

func cleanAssistantAnswer(raw string, hasChart, hasTable bool) string {
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}

	// Always strip fenced code blocks to avoid broken rendering in chat pane.
	reFence := regexp.MustCompile("(?s)```.*?```")
	s = reFence.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "```", "")

	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	lastBlank := false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			if !lastBlank {
				out = append(out, "")
			}
			lastBlank = true
			continue
		}
		lastBlank = false

		// When visual chart/table already exists, drop text-side ASCII charts / markdown tables.
		if hasChart || hasTable {
			if strings.HasPrefix(t, "|") || strings.HasSuffix(t, "|") {
				continue
			}
			if isAsciiArtLine(t) {
				continue
			}
		}
		out = append(out, line)
	}
	cleaned := strings.TrimSpace(strings.Join(out, "\n"))
	cleaned = strings.ReplaceAll(cleaned, "\n\n\n", "\n\n")
	return cleaned
}

func isAsciiArtLine(s string) bool {
	if s == "" {
		return false
	}
	trimmed := strings.TrimSpace(s)
	// Typical line-art tokens and separators used by generated ASCII charts/tables.
	if strings.Contains(trimmed, "┌") || strings.Contains(trimmed, "┐") ||
		strings.Contains(trimmed, "└") || strings.Contains(trimmed, "┘") ||
		strings.Contains(trimmed, "─") || strings.Contains(trimmed, "│") {
		return true
	}
	if strings.Count(trimmed, "|") >= 2 && !regexp.MustCompile(`[A-Za-z0-9一-龥]`).MatchString(trimmed) {
		return true
	}
	if strings.Count(trimmed, "_")+strings.Count(trimmed, "-")+strings.Count(trimmed, "|") >= len(trimmed)-1 && len(trimmed) >= 4 {
		return true
	}
	return false
}

func normalizeMetricList(metrics []string) []string {
	allow := map[string]bool{
		"Revenue": true, "NetIncome": true, "OperatingIncome": true, "RevenueYoY": true,
	}
	out := []string{}
	seen := map[string]bool{}
	for _, m := range metrics {
		k := strings.TrimSpace(m)
		if !allow[k] || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	if len(out) == 0 {
		return []string{"Revenue", "NetIncome", "OperatingIncome"}
	}
	return out
}

func shouldShowCompareChart(message string, plan ComparePlan, results []CompareSeriesData) bool {
	if plan.NeedChart {
		return true
	}
	if regexp.MustCompile(`图|图表|趋势|曲线|可视化|chart|plot`).MatchString(strings.ToLower(message)) {
		return true
	}
	for _, r := range results {
		if len(r.Series["Revenue"]) >= 2 {
			return true
		}
	}
	return false
}

func buildCompareTable(results []CompareSeriesData, metrics []string) any {
	if len(results) == 0 {
		return nil
	}
	metrics = normalizeMetricList(metrics)
	yearSet := map[int]bool{}
	for _, r := range results {
		for _, m := range metrics {
			for _, p := range r.Series[m] {
				yearSet[p.FY] = true
			}
		}
	}
	years := make([]int, 0, len(yearSet))
	for y := range yearSet {
		years = append(years, y)
	}
	sort.Ints(years)
	if len(years) == 0 {
		return nil
	}

	columns := []string{"年度"}
	for _, r := range results {
		for _, m := range metrics {
			columns = append(columns, fmt.Sprintf("%s-%s", r.Target.Name, metricLabel(m)))
		}
	}

	metricIndex := map[string]map[int]float64{}
	for _, r := range results {
		for _, m := range metrics {
			key := r.Target.Symbol + "|" + m
			metricIndex[key] = map[int]float64{}
			for _, p := range r.Series[m] {
				metricIndex[key][p.FY] = p.Value
			}
		}
	}

	rows := make([][]any, 0, len(years))
	for _, y := range years {
		row := []any{y}
		for _, r := range results {
			for _, m := range metrics {
				key := r.Target.Symbol + "|" + m
				if v, ok := metricIndex[key][y]; ok {
					row = append(row, v)
				} else {
					row = append(row, "")
				}
			}
		}
		rows = append(rows, row)
	}

	titleParts := []string{}
	for _, r := range results {
		titleParts = append(titleParts, r.Target.Name)
	}
	return map[string]any{
		"title":   "财务对比表（数据对比） - " + strings.Join(titleParts, " 与 "),
		"columns": columns,
		"rows":    rows,
	}
}

func buildCompareChart(results []CompareSeriesData, metrics []string) any {
	if len(results) == 0 {
		return nil
	}
	metrics = normalizeMetricList(metrics)
	metric := metrics[0]
	for _, prefer := range []string{"Revenue", "NetIncome", "OperatingIncome"} {
		for _, m := range metrics {
			if m == prefer {
				metric = m
				break
			}
		}
	}

	series := []map[string]any{}
	for _, r := range results {
		arr := r.Series[metric]
		if len(arr) == 0 {
			continue
		}
		points := make([]map[string]any, 0, len(arr))
		for _, p := range arr {
			points = append(points, map[string]any{"x": p.FY, "y": p.Value})
		}
		series = append(series, map[string]any{
			"name":   r.Target.Name,
			"points": points,
		})
	}
	if len(series) == 0 {
		return nil
	}

	return map[string]any{
		"title":  "对比趋势 - " + metricLabel(metric),
		"type":   "line",
		"xLabel": "年度",
		"yLabel": metricLabel(metric),
		"series": series,
	}
}

func buildCompareEvidence(results []CompareSeriesData, years []int, metrics []string) string {
	metrics = normalizeMetricList(metrics)
	years = uniqueSortedYears(years)
	lines := []string{
		"【财务对比数据（数据对比）】",
		"口径说明：不同市场币种/会计准则可能不同，比较时关注趋势与相对变化。",
	}
	if len(years) > 0 {
		parts := make([]string, 0, len(years))
		for _, y := range years {
			parts = append(parts, strconv.Itoa(y))
		}
		lines = append(lines, "年份过滤："+strings.Join(parts, ","))
	}
	for _, r := range results {
		lines = append(lines, fmt.Sprintf("公司：%s(%s)", r.Target.Name, r.Target.Symbol))
		for _, m := range metrics {
			arr := r.Series[m]
			if len(arr) == 0 {
				continue
			}
			parts := []string{}
			for _, p := range arr {
				parts = append(parts, fmt.Sprintf("%d:%s", p.FY, formatMetricValue(m, p.Value)))
			}
			lines = append(lines, fmt.Sprintf("- %s: %s", m, strings.Join(parts, " ")))
		}
	}
	return strings.Join(lines, "\n")
}

func latestMetricValue(series []FactPoint) (FactPoint, bool) {
	if len(series) == 0 {
		return FactPoint{}, false
	}
	last := series[0]
	for _, p := range series[1:] {
		if p.FY > last.FY {
			last = p
		}
	}
	return last, true
}

func buildCompareFallbackAnswer(results []CompareSeriesData, years []int, metrics []string, reason string) string {
	metrics = normalizeMetricList(metrics)
	lines := []string{
		"模型总结不可用，已返回基于已查询数据的结构化对比结论。",
	}
	if strings.TrimSpace(reason) != "" {
		lines = append(lines, "原因："+reason)
	}
	for _, r := range results {
		lines = append(lines, fmt.Sprintf("\n%s(%s)", r.Target.Name, r.Target.Symbol))
		for _, m := range metrics {
			if p, ok := latestMetricValue(r.Series[m]); ok {
				lines = append(lines, fmt.Sprintf("- 最新%s(%d): %s", metricLabel(m), p.FY, formatMetricValue(m, p.Value)))
			}
		}
	}
	lines = append(lines, "\n已生成对比表格；如包含趋势关键词也已生成图表。")
	return strings.Join(lines, "\n")
}

func handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
		return
	}
	sessionID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/history/"))
	msgs := getMessages(sessionID)
	if len(msgs) == 0 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<h2>暂无对话数据</h2>"))
		return
	}
	var b strings.Builder
	b.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"/><title>历史对话</title></head><body><h1>历史对话</h1>")
	for _, m := range msgs {
		b.WriteString("<div style=\"border:1px solid #ddd;padding:10px;border-radius:8px;margin:8px 0;\">")
		b.WriteString("<b>" + htmlEsc(m.Role) + "</b><br/>")
		b.WriteString(strings.ReplaceAll(htmlEsc(m.Content), "\n", "<br/>"))
		b.WriteString("</div>")
	}
	b.WriteString("</body></html>")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

func handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
		return
	}
	sessionID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/report/"))
	items := getReportItems(sessionID)
	if len(items) == 0 {
		msgs := getMessages(sessionID)
		if len(msgs) == 0 {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<h2>暂无对话数据</h2>"))
			return
		}
		var q, a string
		for i := len(msgs) - 1; i >= 0; i-- {
			if a == "" && msgs[i].Role == "assistant" {
				a = msgs[i].Content
			}
			if a != "" && msgs[i].Role == "user" {
				q = msgs[i].Content
				break
			}
		}
		html := "<!doctype html><html><head><meta charset=\"utf-8\"/><title>报告</title></head><body><h1>财务分析报告</h1><h3>问题</h3><div>" + strings.ReplaceAll(htmlEsc(q), "\n", "<br/>") + "</div><h3>回答</h3><div>" + strings.ReplaceAll(htmlEsc(a), "\n", "<br/>") + "</div></body></html>"
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
		return
	}

	var b strings.Builder
	b.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"/><title>财务研究报告（草稿）</title>")
	b.WriteString(`<style>
body{font-family:Arial,Helvetica,sans-serif;background:#f8fafc;margin:0;color:#0f172a}
.page{max-width:980px;margin:24px auto 80px;background:#fff;border:1px solid #e2e8f0;border-radius:16px;padding:24px 28px}
.toolbar{display:flex;flex-wrap:wrap;gap:8px;align-items:center;justify-content:space-between;margin-bottom:16px}
.toolbar .left{display:flex;gap:8px;align-items:center}
.btn{padding:8px 12px;border-radius:10px;border:1px solid #cbd5f5;background:#0f172a;color:#fff;cursor:pointer}
.btn.secondary{background:#fff;color:#0f172a}
.muted{color:#64748b;font-size:12px}
.report-item{border:1px solid #e2e8f0;border-radius:14px;padding:16px;margin:14px 0}
.report-item[data-excluded="true"]{opacity:.5}
.item-head{display:flex;flex-wrap:wrap;justify-content:space-between;gap:12px}
.badge{font-size:12px;padding:2px 8px;border-radius:999px;background:#ecfeff;color:#0f766e;border:1px solid #99f6e4}
.controls{display:flex;gap:8px;align-items:center}
.controls .btn{padding:6px 10px;font-size:12px}
.section-title{font-weight:600;margin:12px 0 6px}
.qa{border-left:3px solid #0f172a;padding-left:10px}
.qa-box{border:1px solid #e2e8f0;border-radius:12px;padding:12px;margin-top:8px;background:#f8fafc}
.editable[contenteditable="true"]{outline:2px dashed #94a3b8;background:#f8fafc}
.chart-wrap{border:1px solid #e2e8f0;border-radius:12px;padding:12px;margin-top:10px}
.table-wrap{overflow-x:auto;margin-top:10px}
table{border-collapse:collapse;width:100%;font-size:12px}
th,td{border:1px solid #e2e8f0;padding:6px;text-align:left}
details{margin-top:8px}
@media print{
  body{background:#fff}
  .toolbar,.controls{display:none}
  .report-item[data-excluded="true"]{display:none}
}
</style>`)
	b.WriteString(`</head><body><div class="page">`)
	b.WriteString(`<div class="toolbar">
  <div class="left">
    <button id="btnPrint" class="btn">打印</button>
    <button id="btnToggleAll" class="btn secondary">全部纳入</button>
  </div>
  <div class="muted">Session: ` + htmlEsc(sessionID) + `</div>
</div>`)
	b.WriteString(`<h1 style="margin:0 0 8px;">财务研究报告（草稿）</h1>`)
	b.WriteString(`<div class="muted">以下内容来自本次对话中所有已查询的数据、图表与结论。可编辑、删除或取消纳入后打印。</div>`)

	for i, it := range items {
		itemNo := i + 1
		sourceLabel := reportSourceLabel(it.DataSource)
		b.WriteString(`<section class="report-item" data-id="` + htmlEsc(it.ID) + `" data-excluded="false">`)
		b.WriteString(`<div class="item-head">`)
		b.WriteString(`<div><div class="badge">` + htmlEsc(sourceLabel) + `</div><div class="muted">条目 #` + strconv.Itoa(itemNo) + ` · ` + htmlEsc(it.CreatedAt) + `</div></div>`)
		b.WriteString(`<div class="controls">
  <label class="muted"><input type="checkbox" class="include-toggle" checked> 纳入报告</label>
  <button class="btn secondary" data-action="edit">编辑</button>
  <button class="btn secondary" data-action="delete">删除</button>
</div>`)
		b.WriteString(`</div>`)

		b.WriteString(`<div class="qa-box"><div class="section-title">问题</div>`)
		b.WriteString(`<div class="qa editable" data-field="question">` + strings.ReplaceAll(htmlEsc(it.Question), "\n", "<br/>") + `</div></div>`)
		b.WriteString(`<div class="qa-box"><div class="section-title">结论</div>`)
		b.WriteString(`<div class="qa editable" data-field="answer">` + strings.ReplaceAll(htmlEsc(it.Answer), "\n", "<br/>") + `</div></div>`)

		if strings.TrimSpace(it.Evidence) != "" {
			b.WriteString(`<details><summary class="muted">查看数据明细</summary><pre style="white-space:pre-wrap;background:#f8fafc;border:1px solid #e2e8f0;padding:10px;border-radius:10px;">` + htmlEsc(it.Evidence) + `</pre></details>`)
		}

		if tableHTML := renderReportTable(it.Table); tableHTML != "" {
			b.WriteString(`<div class="section-title">结构化表格</div>`)
			b.WriteString(`<div class="table-wrap">` + tableHTML + `</div>`)
		}

		if chartAttr := renderReportChartAttr(it.Chart); chartAttr != "" {
			b.WriteString(`<div class="section-title">图表</div>`)
			b.WriteString(`<div class="chart-wrap"><canvas height="240" data-chart="` + chartAttr + `"></canvas></div>`)
		}

		if citeHTML := renderReportCitations(it.Citations); citeHTML != "" {
			b.WriteString(`<div class="section-title">引用</div>`)
			b.WriteString(citeHTML)
		}

		b.WriteString(`</section>`)
	}

	b.WriteString(`<script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
<script>
function normalizeChartPayload(raw){
  if(!raw||!Array.isArray(raw.series)||!raw.series.length){return null;}
  const xSet=new Set();
  raw.series.forEach(s=>{(s.points||[]).forEach(p=>xSet.add(p.x));});
  let labels=Array.from(xSet);
  const allNumeric=labels.every(v=>typeof v==="number"||(String(v).trim()!==""&&!Number.isNaN(Number(v))));
  labels=allNumeric?labels.map(v=>Number(v)).sort((a,b)=>a-b):labels.map(v=>String(v));
  const datasets=raw.series.map(s=>{
    const pointsMap=new Map((s.points||[]).map(p=>[allNumeric?Number(p.x):String(p.x),p.y]));
    return {label:s.name,data:labels.map(x=>pointsMap.has(x)?pointsMap.get(x):null)};
  });
  return {type:raw.type||"line",labels,datasets,title:raw.title||"",xLabel:raw.xLabel||"年度",yLabel:raw.yLabel||"数值"};
}
document.querySelectorAll('canvas[data-chart]').forEach((canvas)=>{
  try{
    const raw=JSON.parse(canvas.getAttribute('data-chart'));
    const norm=normalizeChartPayload(raw);
    if(!norm)return;
    const ctx=canvas.getContext('2d');
    new Chart(ctx,{type:norm.type,data:{labels:norm.labels,datasets:norm.datasets},options:{responsive:true,maintainAspectRatio:false,plugins:{title:{display:Boolean(norm.title),text:norm.title}},scales:{x:{title:{display:true,text:norm.xLabel}},y:{title:{display:true,text:norm.yLabel}}}}});
  }catch(e){}
});
document.querySelectorAll('[data-action="delete"]').forEach(btn=>{
  btn.addEventListener('click',()=>{
    const item=btn.closest('.report-item');
    if(item){item.remove();}
  });
});
document.querySelectorAll('[data-action="edit"]').forEach(btn=>{
  btn.addEventListener('click',()=>{
    const item=btn.closest('.report-item');
    if(!item)return;
    const editing=item.getAttribute('data-editing')==='true';
    item.querySelectorAll('.editable').forEach(el=>{el.setAttribute('contenteditable', editing?'false':'true');});
    item.setAttribute('data-editing', editing?'false':'true');
    btn.textContent=editing?'编辑':'完成';
  });
});
document.querySelectorAll('.include-toggle').forEach(cb=>{
  cb.addEventListener('change',()=>{
    const item=cb.closest('.report-item');
    if(item){item.setAttribute('data-excluded', cb.checked?'false':'true');}
  });
});
const btnPrint=document.getElementById('btnPrint');
if(btnPrint){btnPrint.addEventListener('click',()=>window.print());}
const btnToggle=document.getElementById('btnToggleAll');
if(btnToggle){btnToggle.addEventListener('click',()=>{
  const boxes=[...document.querySelectorAll('.include-toggle')];
  const allChecked=boxes.every(b=>b.checked);
  boxes.forEach(b=>{b.checked=!allChecked; b.dispatchEvent(new Event('change'));});
  btnToggle.textContent=allChecked?'全部纳入':'全部取消';
});}
</script>`)
	b.WriteString("</div></body></html>")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
	return
}

func reportSourceLabel(source string) string {
	switch source {
	case "professional":
		return "AkShare + EODHD（数据对比）"
	case "akshare":
		return "AkShare 实时数据"
	case "eodhd":
		return "EODHD 美股数据"
	case "tushare":
		return "Tushare 数据"
	case "local_fallback":
		return "本地回退数据"
	default:
		return "模型回答"
	}
}

func renderReportTable(table any) string {
	if table == nil {
		return ""
	}
	tb, ok := table.(map[string]any)
	if !ok {
		return ""
	}
	cols, ok := tb["columns"].([]string)
	if !ok || len(cols) == 0 {
		return ""
	}
	rows, ok := tb["rows"].([][]any)
	if !ok {
		if rawRows, ok2 := tb["rows"].([]any); ok2 {
			rows = make([][]any, 0, len(rawRows))
			for _, r := range rawRows {
				if arr, ok3 := r.([]any); ok3 {
					rows = append(rows, arr)
				}
			}
		}
	}
	var b strings.Builder
	title := asString(tb["title"])
	if title != "" {
		b.WriteString("<div class=\"muted\" style=\"margin-bottom:6px;\">" + htmlEsc(title) + "</div>")
	}
	b.WriteString("<table><thead><tr>")
	for _, c := range cols {
		b.WriteString("<th>" + htmlEsc(c) + "</th>")
	}
	b.WriteString("</tr></thead><tbody>")
	for _, row := range rows {
		b.WriteString("<tr>")
		for _, cell := range row {
			b.WriteString("<td>" + htmlEsc(fmt.Sprintf("%v", cell)) + "</td>")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table>")
	return b.String()
}

func renderReportCitations(citations []map[string]any) string {
	if len(citations) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<ul style=\"padding-left:18px;margin:6px 0;font-size:12px;color:#475569;\">")
	for _, c := range citations {
		title := htmlEsc(asString(c["title"]))
		url := strings.TrimSpace(asString(c["url"]))
		if url != "" {
			b.WriteString("<li>" + title + ` <a href="` + htmlEsc(url) + `" target="_blank" rel="noreferrer">打开</a></li>`)
		} else {
			b.WriteString("<li>" + title + "</li>")
		}
	}
	b.WriteString("</ul>")
	return b.String()
}

func renderReportChartAttr(chart any) string {
	if chart == nil {
		return ""
	}
	raw, err := json.Marshal(chart)
	if err != nil {
		return ""
	}
	return htmlEsc(string(raw))
}

func htmlEsc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#039;")
	return r.Replace(s)
}

func isFinancialQuestion(message string) bool {
	re := regexp.MustCompile(`营收|收入|利润|毛利|财务|研发|同比|环比|趋势|报表|净利|资产|负债|业绩|指标|现金流|数据|图表|Revenue|Income|Profit|Margin`)
	return re.MatchString(message)
}

func extractYears(message string) []int {
	re := regexp.MustCompile(`20\d{2}`)
	matches := re.FindAllString(message, -1)
	seen := map[int]bool{}
	out := []int{}
	for _, m := range matches {
		y, err := strconv.Atoi(m)
		if err != nil {
			continue
		}
		if !seen[y] {
			seen[y] = true
			out = append(out, y)
		}
	}
	sort.Ints(out)
	return out
}

func parseYearsCSV(v string) []int {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := []int{}
	seen := map[int]bool{}
	for _, p := range parts {
		y, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			continue
		}
		if !seen[y] {
			seen[y] = true
			out = append(out, y)
		}
	}
	sort.Ints(out)
	return out
}

func uniqueSortedYears(years []int) []int {
	seen := map[int]bool{}
	out := []int{}
	for _, y := range years {
		if y <= 0 {
			continue
		}
		if !seen[y] {
			seen[y] = true
			out = append(out, y)
		}
	}
	sort.Ints(out)
	return out
}

func isCompareQuestion(message string) bool {
	m := strings.ToLower(message)
	hasCompareWord := regexp.MustCompile(`对比|比较|vs|v\.s\.|versus|与|和|两个|两家|前二|前两|top\s*2|top2|前2`).MatchString(m)
	hasFinanceWord := regexp.MustCompile(`财报|财务|营收|收入|利润|净利|报表|年报|Revenue|Income|Profit|Margin`).MatchString(message)
	return hasCompareWord && (hasFinanceWord || regexp.MustCompile(`近\d+年|近几年|趋势`).MatchString(message))
}

func hasCompareHint(message string) bool {
	m := strings.ToLower(strings.TrimSpace(message))
	return regexp.MustCompile(`对比|比较|vs|v\.s\.|versus|与|和|两个|两家|前二|前两|top\s*2|top2|前2`).MatchString(m)
}

func buildHeuristicComparePlan(message string, defaultYears []int) ComparePlan {
	years := uniqueSortedYears(defaultYears)
	if len(years) == 0 {
		nowYear := time.Now().Year()
		years = []int{nowYear - 4, nowYear - 3, nowYear - 2, nowYear - 1, nowYear}
	}
	metrics := []string{"Revenue", "NetIncome", "OperatingIncome"}
	needChart := regexp.MustCompile(`图|图表|趋势|曲线|可视化|chart|plot`).MatchString(strings.ToLower(message))
	return ComparePlan{
		Targets:   nil,
		Years:     years,
		Metrics:   metrics,
		NeedChart: needChart,
		NeedTable: true,
	}
}

func mergeComparePlan(base, fromLLM ComparePlan) ComparePlan {
	out := base
	if len(fromLLM.Targets) > 0 {
		out.Targets = fromLLM.Targets
	}
	if ys := uniqueSortedYears(fromLLM.Years); len(ys) > 0 {
		out.Years = ys
	}
	if len(fromLLM.Metrics) > 0 {
		out.Metrics = fromLLM.Metrics
	}
	if fromLLM.NeedChart {
		out.NeedChart = true
	}
	if fromLLM.NeedTable {
		out.NeedTable = true
	}
	return out
}

func callGLMComparePlan(apiKey, message string, fallbackYears []int) (ComparePlan, error) {
	system := "你是金融数据查询规划器。根据用户问题，输出调用 AkShare（A股）和 EODHD（美股）的结构化计划。只输出 JSON，不要解释。"
	user := "请输出 JSON，字段如下：" +
		`{"targets":["标的1","标的2"],"years":[2021,2022,2023],"metrics":["Revenue","NetIncome"],"needChart":true,"needTable":true}` +
		"\n要求：1) targets 至少2个；2) years 为空时给近5年；3) metrics 仅允许 Revenue/NetIncome/OperatingIncome/RevenueYoY。\n用户问题：" + message

	raw, err := callGLMRaw(apiKey, system, user, 0.1, 600)
	if err != nil {
		return ComparePlan{}, err
	}
	block := extractJSONBlock(raw)
	if block == "" {
		return ComparePlan{}, fmt.Errorf("planner non-json response")
	}
	var plan ComparePlan
	if err := json.Unmarshal([]byte(block), &plan); err != nil {
		return ComparePlan{}, err
	}
	plan.Years = uniqueSortedYears(plan.Years)
	if len(plan.Years) == 0 {
		plan.Years = uniqueSortedYears(fallbackYears)
	}
	if len(plan.Metrics) == 0 {
		plan.Metrics = []string{"Revenue", "NetIncome", "OperatingIncome"}
	}
	return plan, nil
}

func callGLMResolveCompareTargets(apiKey, message string) ([]CompareTarget, error) {
	system := "你是金融标的解析器。把用户问题中的公司名解析成可查询标的。A股用6位代码+交易所后缀（如 300750.SZ、600519.SH），美股用Ticker（如 TSLA、BABA），港股用 0700.HK。若用户未明确公司名但要求行业前两名/头部公司，请基于常识给出2家代表公司并补全代码。只输出 JSON。"
	user := "输出 JSON：\n" +
		`{"targets":[{"name":"公司名","symbol":"300750.SZ","market":"CN"},{"name":"公司名","symbol":"TSLA","market":"US"}]}` +
		"\n要求：1) 至少2个标的（单公司问题可返回1个）；2) market 仅允许 CN/US；3) 不要解释。\n用户问题：" + message

	raw, err := callGLMRaw(apiKey, system, user, 0.1, 500)
	if err != nil {
		return nil, err
	}
	block := extractJSONBlock(raw)
	if block == "" {
		return nil, fmt.Errorf("target resolver non-json response")
	}
	var parsed struct {
		Targets []struct {
			Name   string `json:"name"`
			Symbol string `json:"symbol"`
			Market string `json:"market"`
		} `json:"targets"`
	}
	if err := json.Unmarshal([]byte(block), &parsed); err != nil {
		return nil, err
	}
	out := []CompareTarget{}
	seen := map[string]bool{}
	for _, t := range parsed.Targets {
		name := strings.TrimSpace(t.Name)
		symbol := strings.ToUpper(strings.TrimSpace(t.Symbol))
		market := strings.ToUpper(strings.TrimSpace(t.Market))
		if market != "CN" && market != "US" {
			continue
		}
		if market == "CN" {
			if m := regexp.MustCompile(`\b\d{6}\.(?:SZ|SH)\b`).FindString(symbol); m != "" {
				symbol = m
			} else {
				continue
			}
		}
		if market == "US" {
			switch {
			case regexp.MustCompile(`^[A-Z]{1,8}$`).MatchString(symbol):
				if isStopTickerToken(symbol) {
					continue
				}
			case regexp.MustCompile(`^\d{4}\.HK$`).MatchString(symbol):
				// accepted HK ticker routed to EODHD in professional mode
			default:
				continue
			}
		}
		if name == "" {
			name = symbol
		}
		key := market + ":" + symbol
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, CompareTarget{
			Name:   name,
			Symbol: symbol,
			Market: market,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("target resolver empty")
	}
	return out, nil
}

func extractJSONBlock(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return ""
}

func resolveCNStockFromQuestion(question string) (code string, name string, ok bool) {
	qUpper := strings.ToUpper(question)
	if m := regexp.MustCompile(`\b\d{6}\.(?:SZ|SH)\b`).FindString(qUpper); m != "" {
		return m, m, true
	}
	if m := regexp.MustCompile(`\b\d{6}\b`).FindString(qUpper); m != "" {
		// 经验规则：A股 6 开头多为 SH，其他多为 SZ。
		if strings.HasPrefix(m, "6") {
			return m + ".SH", m, true
		}
		return m + ".SZ", m, true
	}

	stocks, err := loadCNStocks()
	if err != nil || len(stocks) == 0 {
		return "", "", false
	}

	bestScore := -1
	bestCode := ""
	bestName := ""
	for _, s := range stocks {
		score := 0
		nameUpper := strings.ToUpper(s.Name)
		codeUpper := strings.ToUpper(s.Code)
		if strings.Contains(qUpper, codeUpper) {
			score += 100
		}
		if strings.Contains(qUpper, nameUpper) {
			score += 80
		}
		shortName := regexp.MustCompile(`股份有限(公司)?|有限公司`).ReplaceAllString(nameUpper, "")
		if shortName != "" && strings.Contains(qUpper, shortName) {
			score += 60
		}
		if score > bestScore {
			bestScore = score
			bestCode = s.Code
			bestName = s.Name
		}
	}
	if bestScore <= 0 {
		return "", "", false
	}
	return strings.ToUpper(bestCode), bestName, true
}

func loadCNStocks() ([]CNStock, error) {
	dataDir := strings.TrimSpace(getenv("DATA_DIR", "../data"))
	candidates := []string{
		filepath.Join(dataDir, "cn_stocks.json"),
		filepath.Join("..", "data", "cn_stocks.json"),
		filepath.Join("data", "cn_stocks.json"),
		filepath.Join(".", "data", "cn_stocks.json"),
	}
	var lastErr error
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err != nil {
			lastErr = err
			continue
		}
		b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
		var data CNStockFile
		if err := json.Unmarshal(b, &data); err != nil {
			lastErr = err
			continue
		}
		if len(data.Stocks) > 0 {
			return data.Stocks, nil
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("cn_stocks.json not found")
	}
	return nil, lastErr
}

func lookupCNNameByCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return ""
	}
	if !regexp.MustCompile(`\d{6}\.(SZ|SH)$`).MatchString(code) {
		return ""
	}
	stocks, err := loadCNStocks()
	if err != nil {
		return ""
	}
	for _, s := range stocks {
		if strings.EqualFold(s.Code, code) {
			return s.Name
		}
	}
	return ""
}

func enrichTargetName(t CompareTarget) CompareTarget {
	name := strings.TrimSpace(t.Name)
	symbol := strings.ToUpper(strings.TrimSpace(t.Symbol))
	t.Symbol = symbol
	if name == "" || strings.EqualFold(name, symbol) {
		if t.Market == "CN" {
			if n := lookupCNNameByCode(symbol); n != "" {
				name = n
			}
		}
	}
	if name == "" {
		name = symbol
	}
	t.Name = name
	return t
}

func enrichTargetNames(items []CompareTarget) []CompareTarget {
	out := make([]CompareTarget, 0, len(items))
	for _, t := range items {
		out = append(out, enrichTargetName(t))
	}
	return out
}

func defaultAliasTargets() map[string]CompareTarget {
	return map[string]CompareTarget{
		"比亚迪":  {Name: "比亚迪", Symbol: "002594.SZ", Market: "CN"},
		"宁德时代": {Name: "宁德时代", Symbol: "300750.SZ", Market: "CN"},
		"茅台":   {Name: "贵州茅台", Symbol: "600519.SH", Market: "CN"},
		"腾讯":   {Name: "腾讯控股", Symbol: "0700.HK", Market: "US"},
		"腾讯控股": {Name: "腾讯控股", Symbol: "0700.HK", Market: "US"},
		"阿里":   {Name: "阿里巴巴", Symbol: "BABA", Market: "US"},
		"阿里巴巴": {Name: "阿里巴巴", Symbol: "BABA", Market: "US"},
		"网易":   {Name: "网易", Symbol: "NTES", Market: "US"},
		"网易公司": {Name: "网易", Symbol: "NTES", Market: "US"},
		"网易-S": {Name: "网易", Symbol: "NTES", Market: "US"},
		"特斯拉":  {Name: "TSLA", Symbol: "TSLA", Market: "US"},
		"苹果":   {Name: "AAPL", Symbol: "AAPL", Market: "US"},
		"微软":   {Name: "MSFT", Symbol: "MSFT", Market: "US"},
		"英伟达":  {Name: "NVDA", Symbol: "NVDA", Market: "US"},
		"亚马逊":  {Name: "AMZN", Symbol: "AMZN", Market: "US"},
		"谷歌":   {Name: "GOOGL", Symbol: "GOOGL", Market: "US"},
	}
}

func mergeCompareTargetLists(primary []CompareTarget, fallback ...[]CompareTarget) []CompareTarget {
	out := make([]CompareTarget, 0, len(primary))
	seen := map[string]bool{}
	appendList := func(items []CompareTarget) {
		for _, t := range items {
			symbol := strings.ToUpper(strings.TrimSpace(t.Symbol))
			if symbol == "" {
				continue
			}
			market := strings.ToUpper(strings.TrimSpace(t.Market))
			if market != "CN" && market != "US" {
				if regexp.MustCompile(`\d{6}\.(SZ|SH)$`).MatchString(symbol) {
					market = "CN"
				} else {
					market = "US"
				}
			}
			key := market + ":" + symbol
			if seen[key] {
				continue
			}
			seen[key] = true
			name := strings.TrimSpace(t.Name)
			if name == "" {
				name = symbol
			}
			out = append(out, CompareTarget{
				Name:   name,
				Symbol: symbol,
				Market: market,
			})
		}
	}
	appendList(primary)
	for _, items := range fallback {
		appendList(items)
	}
	return out
}

func resolveCompareTargets(message string, planTargets []string) []CompareTarget {
	rawCandidates := []string{}
	rawCandidates = append(rawCandidates, planTargets...)
	rawCandidates = append(rawCandidates, extractSymbolCandidates(message)...)

	aliases := defaultAliasTargets()
	for alias := range aliases {
		if strings.Contains(strings.ToLower(message), strings.ToLower(alias)) {
			rawCandidates = append(rawCandidates, alias)
		}
	}

	out := []CompareTarget{}
	seen := map[string]bool{}
	appendTarget := func(t CompareTarget) {
		t = enrichTargetName(t)
		key := strings.ToUpper(strings.TrimSpace(t.Symbol))
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, t)
	}

	for _, raw := range rawCandidates {
		if t, ok := resolveSingleTarget(raw); ok {
			appendTarget(t)
		}
	}

	if len(out) < 2 {
		// Try to supplement from CN stock name matches in the original message.
		stocks, err := loadCNStocks()
		if err == nil {
			for _, s := range stocks {
				if strings.Contains(message, s.Name) {
					appendTarget(CompareTarget{Name: s.Name, Symbol: strings.ToUpper(s.Code), Market: "CN"})
				}
				if len(out) >= 2 {
					break
				}
			}
		}
	}

	return out
}

func resolveSingleTarget(raw string) (CompareTarget, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return CompareTarget{}, false
	}
	aliases := defaultAliasTargets()
	if t, ok := aliases[s]; ok {
		return t, true
	}

	upper := strings.ToUpper(s)
	if m := regexp.MustCompile(`^\d{4}\.HK$`).FindString(upper); m != "" {
		return CompareTarget{Name: m, Symbol: m, Market: "US"}, true
	}
	if m := regexp.MustCompile(`^\d{4}$`).FindString(upper); m != "" {
		return CompareTarget{Name: m, Symbol: m + ".HK", Market: "US"}, true
	}
	if m := regexp.MustCompile(`\b\d{6}\.(?:SZ|SH)\b`).FindString(upper); m != "" {
		return CompareTarget{Name: m, Symbol: m, Market: "CN"}, true
	}
	if m := regexp.MustCompile(`\b\d{6}\b`).FindString(upper); m != "" {
		if strings.HasPrefix(m, "6") {
			return CompareTarget{Name: m, Symbol: m + ".SH", Market: "CN"}, true
		}
		return CompareTarget{Name: m, Symbol: m + ".SZ", Market: "CN"}, true
	}
	if regexp.MustCompile(`^[A-Z]{1,8}$`).MatchString(upper) && !isStopTickerToken(upper) {
		return CompareTarget{Name: upper, Symbol: upper, Market: "US"}, true
	}

	// Fallback: fuzzy match CN name.
	stocks, err := loadCNStocks()
	if err == nil {
		bestScore := -1
		var best CNStock
		for _, st := range stocks {
			score := 0
			if strings.EqualFold(st.Name, s) {
				score += 100
			}
			if strings.Contains(st.Name, s) || strings.Contains(s, st.Name) {
				score += 50
			}
			if strings.EqualFold(st.Code, upper) {
				score += 80
			}
			if score > bestScore {
				bestScore = score
				best = st
			}
		}
		if bestScore > 0 {
			return CompareTarget{Name: best.Name, Symbol: strings.ToUpper(best.Code), Market: "CN"}, true
		}
	}

	return CompareTarget{}, false
}

func resolveFinancialTarget(message string) (CompareTarget, bool) {
	if code, name, ok := resolveCNStockFromQuestion(message); ok {
		return enrichTargetName(CompareTarget{Name: name, Symbol: code, Market: "CN"}), true
	}
	// Fallback to generic target resolver (supports US tickers like TSLA/AAPL).
	targets := resolveCompareTargets(message, nil)
	if len(targets) == 0 {
		return CompareTarget{}, false
	}
	return enrichTargetName(targets[0]), true
}

func isStopTickerToken(s string) bool {
	stop := map[string]bool{
		"VS": true, "AND": true, "OR": true, "THE": true, "TOP": true, "A": true, "B": true,
	}
	return stop[s]
}

var lastTargetsMu sync.RWMutex
var lastTargets = map[string][]CompareTarget{}

func setLastTargets(sessionID string, targets []CompareTarget) {
	if strings.TrimSpace(sessionID) == "" || len(targets) == 0 {
		return
	}
	cp := make([]CompareTarget, 0, len(targets))
	seen := map[string]bool{}
	for _, t := range targets {
		t = enrichTargetName(t)
		key := strings.ToUpper(strings.TrimSpace(t.Symbol))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		cp = append(cp, t)
		if len(cp) >= 3 {
			break
		}
	}
	if len(cp) == 0 {
		return
	}
	lastTargetsMu.Lock()
	lastTargets[sessionID] = cp
	lastTargetsMu.Unlock()
}

func getLastTargets(sessionID string) []CompareTarget {
	lastTargetsMu.RLock()
	defer lastTargetsMu.RUnlock()
	items := lastTargets[sessionID]
	out := make([]CompareTarget, len(items))
	copy(out, items)
	return out
}

func shouldUseMemoryV2(message string) bool {
	return regexp.MustCompile(`(?i)以上|上面|之前|刚才|刚刚|上述|根据|结合|这些数据|前面|上文|前述|先前|继续|进一步`).MatchString(message)
}

func isBroadCategoryQuery(message string) bool {
	if strings.TrimSpace(message) == "" {
		return false
	}
	hasDomain := regexp.MustCompile(`行业|产业|赛道|板块|概念|主题|领域`).MatchString(message)
	if !hasDomain {
		return false
	}
	lower := strings.ToLower(message)
	return regexp.MustCompile(`最|头部|龙头|前[二两2]|top\s*2|top2|两家|两个`).MatchString(lower)
}

func shouldCarryTargetsV2(message string) bool {
	if shouldUseMemoryV2(message) {
		return true
	}
	// 避免“行业/产业 top2”这类新问题被上一轮标的污染。
	if isBroadCategoryQuery(message) {
		return false
	}
	return regexp.MustCompile(`(?i)这两个|这两家|分别|前者|后者|二者|两者|它们|他们|上述两家|上面两家`).MatchString(message)
}

func applyMarketConstraintByQuestion(message string, targets []CompareTarget) []CompareTarget {
	if len(targets) == 0 {
		return targets
	}
	msg := strings.ToLower(strings.TrimSpace(message))
	if msg == "" {
		return targets
	}
	hasCNHint := regexp.MustCompile(`国内|中国|a股|沪深|内地|本土`).MatchString(msg)
	hasUSHint := regexp.MustCompile(`美股|美国|纳斯达克|纽交所|nyse|nasdaq|us\s*stock`).MatchString(msg)
	if hasCNHint && !hasUSHint {
		out := make([]CompareTarget, 0, len(targets))
		for _, t := range targets {
			s := strings.ToUpper(strings.TrimSpace(t.Symbol))
			if regexp.MustCompile(`\d{6}\.(SZ|SH)$`).MatchString(s) {
				out = append(out, t)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if hasUSHint && !hasCNHint {
		out := make([]CompareTarget, 0, len(targets))
		for _, t := range targets {
			s := strings.ToUpper(strings.TrimSpace(t.Symbol))
			if regexp.MustCompile(`^[A-Z]{1,8}$`).MatchString(s) || regexp.MustCompile(`^\d{4}\.HK$`).MatchString(s) {
				out = append(out, t)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return targets
}

func collectMemoryEvidenceV2(sessionID string, maxItems, maxChars int) string {
	items := getReportItems(sessionID)
	if len(items) == 0 {
		return ""
	}
	if maxItems <= 0 {
		maxItems = 3
	}
	if maxChars <= 0 {
		maxChars = 1200
	}
	parts := []string{}
	used := 0
	for i := len(items) - 1; i >= 0 && len(parts) < maxItems; i-- {
		ev := strings.TrimSpace(items[i].Evidence)
		if ev == "" {
			continue
		}
		limit := maxChars - used
		if limit <= 0 {
			break
		}
		chunk := truncateRunes(ev, limit)
		title := strings.TrimSpace(items[i].Question)
		if title != "" {
			parts = append(parts, "问题："+title+"\n"+chunk)
		} else {
			parts = append(parts, chunk)
		}
		used += len([]rune(chunk))
	}
	return strings.Join(parts, "\n\n")
}

func collectMemorySummaryV2(sessionID string, maxItems, maxChars int) string {
	items := getReportItems(sessionID)
	if len(items) == 0 {
		return ""
	}
	if maxItems <= 0 {
		maxItems = 4
	}
	if maxChars <= 0 {
		maxChars = 900
	}
	parts := []string{}
	used := 0
	for i := len(items) - 1; i >= 0 && len(parts) < maxItems; i-- {
		if strings.TrimSpace(items[i].Evidence) == "" && strings.EqualFold(strings.TrimSpace(items[i].DataSource), "glm_only") {
			continue
		}
		q := compactTextInline(items[i].Question)
		a := compactTextInline(items[i].Answer)
		if q == "" && a == "" {
			continue
		}
		piece := "问：" + truncateRunes(q, 80)
		if a != "" {
			piece += "；答：" + truncateRunes(a, 120)
		}
		limit := maxChars - used
		if limit <= 0 {
			break
		}
		piece = truncateRunes(piece, limit)
		parts = append(parts, piece)
		used += len([]rune(piece))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}

func compactTextInline(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.Join(strings.Fields(s), " ")
}

func mergeLLMEvidence(currentEvidence, memorySummary, memoryEvidence string) string {
	blocks := []string{}
	memorySummary = strings.TrimSpace(memorySummary)
	memoryEvidence = strings.TrimSpace(memoryEvidence)
	currentEvidence = strings.TrimSpace(currentEvidence)
	if memorySummary != "" {
		blocks = append(blocks, "【历史对话摘要】\n"+memorySummary)
	}
	if memoryEvidence != "" {
		blocks = append(blocks, "【历史数据证据】\n"+memoryEvidence)
	}
	if currentEvidence != "" {
		blocks = append(blocks, "【当前查询证据】\n"+currentEvidence)
	}
	return strings.Join(blocks, "\n\n")
}

func hasExplicitTargets(message string) bool {
	if len(extractSymbolCandidates(message)) > 0 {
		return true
	}
	aliases := defaultAliasTargets()
	low := strings.ToLower(message)
	for alias := range aliases {
		if strings.Contains(low, strings.ToLower(alias)) {
			return true
		}
	}
	return false
}

func joinTargetHint(targets []CompareTarget) string {
	parts := []string{}
	for _, t := range targets {
		name := strings.TrimSpace(t.Name)
		symbol := strings.TrimSpace(t.Symbol)
		if name == "" {
			name = symbol
		}
		if symbol != "" && name != symbol {
			parts = append(parts, fmt.Sprintf("%s(%s)", name, symbol))
		} else if name != "" {
			parts = append(parts, name)
		}
		if len(parts) >= 3 {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	if len(parts) == 2 {
		return parts[0] + " 和 " + parts[1]
	}
	return strings.Join(parts[:len(parts)-1], "、") + " 和 " + parts[len(parts)-1]
}

func targetsFromResults(results []CompareSeriesData) []CompareTarget {
	out := make([]CompareTarget, 0, len(results))
	for _, r := range results {
		out = append(out, r.Target)
	}
	return out
}

func extractSymbolCandidates(message string) []string {
	out := []string{}
	seen := map[string]bool{}
	appendUnique := func(v string) {
		key := strings.ToUpper(strings.TrimSpace(v))
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, v)
	}
	for _, m := range regexp.MustCompile(`\b\d{6}\.(?:SZ|SH)\b`).FindAllString(strings.ToUpper(message), -1) {
		appendUnique(m)
	}
	for _, m := range regexp.MustCompile(`\b\d{4}\.HK\b`).FindAllString(strings.ToUpper(message), -1) {
		appendUnique(m)
	}
	for _, m := range regexp.MustCompile(`\b\d{6}\b`).FindAllString(strings.ToUpper(message), -1) {
		appendUnique(m)
	}
	for _, m := range regexp.MustCompile(`\b[A-Z]{2,8}\b`).FindAllString(strings.ToUpper(message), -1) {
		if !isStopTickerToken(m) {
			appendUnique(m)
		}
	}
	return out
}

func getCNFinanceFromAkshare(code string, years []int) (map[string][]FactPoint, string, error) {
	yearParts := make([]string, 0, len(years))
	for _, y := range years {
		yearParts = append(yearParts, strconv.Itoa(y))
	}
	out, err := runAkshareBridge("--mode", "finance", "--code", code, "--years", strings.Join(yearParts, ","))
	if err != nil {
		return nil, "", err
	}
	var resp struct {
		Series map[string][]FactPoint `json:"series"`
		Error  string                 `json:"error"`
		Source string                 `json:"source_url"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(resp.Error) != "" {
		return nil, "", fmt.Errorf("%s", resp.Error)
	}
	if len(resp.Series) == 0 {
		return nil, "", fmt.Errorf("akshare finance empty")
	}
	for k := range resp.Series {
		sort.Slice(resp.Series[k], func(i, j int) bool { return resp.Series[k][i].FY < resp.Series[k][j].FY })
	}
	source := strings.TrimSpace(resp.Source)
	if source == "" {
		source = "https://akshare.akfamily.xyz/data/stock/stock.html"
	}
	return resp.Series, source, nil
}

func normalizeEODHDSymbol(symbol string) (string, error) {
	s := strings.ToUpper(strings.TrimSpace(symbol))
	if s == "" {
		return "", fmt.Errorf("empty symbol")
	}
	if regexp.MustCompile(`^[A-Z]{1,8}\.(US|HK)$`).MatchString(s) {
		return s, nil
	}
	if regexp.MustCompile(`^\d{4}\.HK$`).MatchString(s) {
		return s, nil
	}
	if regexp.MustCompile(`^[A-Z]{1,8}$`).MatchString(s) {
		return s + ".US", nil
	}
	if regexp.MustCompile(`^\d{4}$`).MatchString(s) {
		return s + ".HK", nil
	}
	return "", fmt.Errorf("invalid eodhd symbol: %s", symbol)
}

func selectAkshareFallbackCode(target CompareTarget) (string, bool) {
	symbol := strings.ToUpper(strings.TrimSpace(target.Symbol))
	name := strings.TrimSpace(target.Name)
	if symbol == "" {
		return "", false
	}

	// EODHD accepts HK code; AkShare美股端点更适合纯Ticker。
	if strings.HasSuffix(symbol, ".US") {
		base := strings.TrimSuffix(symbol, ".US")
		if regexp.MustCompile(`^[A-Z]{1,8}$`).MatchString(base) && !isStopTickerToken(base) {
			return base, true
		}
	}
	if regexp.MustCompile(`^[A-Z]{1,8}$`).MatchString(symbol) && !isStopTickerToken(symbol) {
		return symbol, true
	}
	if symbol == "0700.HK" || symbol == "0700" || strings.Contains(name, "腾讯") {
		return "00700", true
	}
	if symbol == "9988.HK" || symbol == "9988" || strings.Contains(name, "阿里") {
		return "BABA", true
	}
	if symbol == "9999.HK" || symbol == "9999" || strings.Contains(name, "网易") {
		return "NTES", true
	}
	return "", false
}

func buildEODHDTraceURL(symbol string, years []int) string {
	base := "/api/source/eodhd"
	yearParts := make([]string, 0, len(years))
	for _, y := range years {
		yearParts = append(yearParts, strconv.Itoa(y))
	}
	q := url.Values{}
	norm, err := normalizeEODHDSymbol(symbol)
	if err != nil {
		norm = strings.ToUpper(strings.TrimSpace(symbol))
	}
	q.Set("symbol", norm)
	if len(yearParts) > 0 {
		q.Set("years", strings.Join(yearParts, ","))
	}
	return base + "?" + q.Encode()
}

func parseFinancialNumber(v any) (float64, bool) {
	if f, ok := asFloat(v); ok {
		return f, true
	}
	s := strings.ReplaceAll(asString(v), ",", "")
	return asFloat(s)
}

func pickNumberFromAnyMap(row map[string]any, keys []string) (float64, bool) {
	for _, k := range keys {
		if v, ok := row[k]; ok {
			if f, ok := parseFinancialNumber(v); ok {
				return f, true
			}
		}
	}
	return 0, false
}

func getUSFinanceFromEODHD(symbol string, years []int) (map[string][]FactPoint, string, error) {
	apiKey := strings.TrimSpace(os.Getenv("EODHD_API_KEY"))
	if apiKey == "" {
		return nil, "", fmt.Errorf("missing EODHD_API_KEY")
	}
	baseURL := strings.TrimSpace(os.Getenv("EODHD_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://eodhd.com/api"
	}
	eodhdSymbol, normErr := normalizeEODHDSymbol(symbol)
	if normErr != nil {
		return nil, "", normErr
	}
	u := fmt.Sprintf(
		"%s/fundamentals/%s?api_token=%s&fmt=json",
		strings.TrimRight(baseURL, "/"),
		url.QueryEscape(eodhdSymbol),
		url.QueryEscape(apiKey),
	)
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	resp, err := glmHTTPClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("%s", sanitizeEODHDError(err.Error()))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("%s", sanitizeEODHDError(fmt.Sprintf("eodhd status=%d body=%s", resp.StatusCode, string(body))))
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, "", err
	}

	currency := "USD"
	if g, ok := raw["General"].(map[string]any); ok {
		if c := strings.TrimSpace(asString(g["CurrencyCode"])); c != "" {
			currency = c
		}
	}

	fin, ok := raw["Financials"].(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("eodhd fundamentals missing Financials")
	}
	isMap, ok := fin["Income_Statement"].(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("eodhd fundamentals missing Income_Statement")
	}
	yearly, ok := isMap["yearly"].(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("eodhd fundamentals missing yearly")
	}

	yearSet := map[int]bool{}
	for _, y := range years {
		yearSet[y] = true
	}

	series := map[string][]FactPoint{}
	appendMetric := func(metric string, year int, value float64) {
		if len(yearSet) > 0 && !yearSet[year] {
			return
		}
		series[metric] = append(series[metric], FactPoint{
			FY:    year,
			Value: value,
			Unit:  currency,
		})
	}

	for dateKey, rec := range yearly {
		row, ok := rec.(map[string]any)
		if !ok {
			continue
		}
		year := 0
		if len(dateKey) >= 4 {
			year, _ = strconv.Atoi(dateKey[:4])
		}
		if year <= 0 {
			if d := strings.TrimSpace(asString(row["date"])); len(d) >= 4 {
				year, _ = strconv.Atoi(d[:4])
			}
		}
		if year <= 0 {
			continue
		}

		if v, ok := pickNumberFromAnyMap(row, []string{"totalRevenue", "TotalRevenue", "revenue", "Revenue"}); ok {
			appendMetric("Revenue", year, v)
		}
		if v, ok := pickNumberFromAnyMap(row, []string{"netIncome", "NetIncome", "netIncomeCommonStockholders"}); ok {
			appendMetric("NetIncome", year, v)
		}
		if v, ok := pickNumberFromAnyMap(row, []string{"operatingIncome", "OperatingIncome", "incomeFromOperations"}); ok {
			appendMetric("OperatingIncome", year, v)
		}
	}

	for k := range series {
		sort.Slice(series[k], func(i, j int) bool { return series[k][i].FY < series[k][j].FY })
	}

	if rev := series["Revenue"]; len(rev) > 1 {
		yoy := make([]FactPoint, 0, len(rev)-1)
		for i := 1; i < len(rev); i++ {
			prev := rev[i-1]
			cur := rev[i]
			if prev.Value == 0 {
				continue
			}
			yoy = append(yoy, FactPoint{
				FY:    cur.FY,
				Value: (cur.Value - prev.Value) / prev.Value,
				Unit:  "ratio",
			})
		}
		if len(yoy) > 0 {
			series["RevenueYoY"] = yoy
		}
	}

	if len(series) == 0 {
		return nil, "", fmt.Errorf("eodhd parsed series empty for %s", eodhdSymbol)
	}
	return series, "https://eodhd.com/financial-apis/", nil
}

func getCNFinanceFromTushare(code string, years []int) (map[string][]FactPoint, string, error) {
	token := getTushareToken()
	if token == "" {
		return nil, "", fmt.Errorf("missing TUSHARE_TOKEN (also checked TUSHARE_API_KEY/TUSHARE_KEY)")
	}
	apiURL := getTushareAPIURL()
	if apiURL == "" {
		apiURL = "http://api.tushare.pro"
	}

	startDate := ""
	endDate := ""
	if len(years) > 0 {
		minY := years[0]
		maxY := years[0]
		for _, y := range years[1:] {
			if y < minY {
				minY = y
			}
			if y > maxY {
				maxY = y
			}
		}
		startDate = fmt.Sprintf("%d0101", minY)
		endDate = fmt.Sprintf("%d1231", maxY)
	}

	incomeRows, err := callTushare(apiURL, token, "income", map[string]any{
		"ts_code":    code,
		"start_date": startDate,
		"end_date":   endDate,
		"fields":     "ts_code,end_date,revenue,operate_profit,n_income,rd_exp",
	})
	if err != nil {
		return nil, "", err
	}
	balanceRows, err := callTushare(apiURL, token, "balancesheet", map[string]any{
		"ts_code":    code,
		"start_date": startDate,
		"end_date":   endDate,
		"fields":     "ts_code,end_date,total_assets,total_liab",
	})
	if err != nil {
		// 资产负债表失败不阻断主流程。
		balanceRows = nil
	}

	yearSet := map[int]bool{}
	for _, y := range years {
		yearSet[y] = true
	}

	series := map[string][]FactPoint{}
	appendMetric := func(metric string, year int, value float64, unit string) {
		if len(yearSet) > 0 && !yearSet[year] {
			return
		}
		series[metric] = append(series[metric], FactPoint{
			FY:    year,
			Value: value,
			Unit:  unit,
		})
	}

	for _, row := range incomeRows {
		endDate := asString(row["end_date"])
		if !strings.HasSuffix(endDate, "1231") {
			continue
		}
		year, _ := strconv.Atoi(endDate[:4])
		if v, ok := asFloat(row["revenue"]); ok {
			appendMetric("Revenue", year, v, "CNY")
		}
		if v, ok := asFloat(row["operate_profit"]); ok {
			appendMetric("OperatingIncome", year, v, "CNY")
		}
		if v, ok := asFloat(row["n_income"]); ok {
			appendMetric("NetIncome", year, v, "CNY")
		}
		if v, ok := asFloat(row["rd_exp"]); ok {
			appendMetric("RAndD", year, v, "CNY")
		}
	}

	for _, row := range balanceRows {
		endDate := asString(row["end_date"])
		if !strings.HasSuffix(endDate, "1231") {
			continue
		}
		year, _ := strconv.Atoi(endDate[:4])
		if v, ok := asFloat(row["total_assets"]); ok {
			appendMetric("TotalAssets", year, v, "CNY")
		}
		if v, ok := asFloat(row["total_liab"]); ok {
			appendMetric("TotalLiabilities", year, v, "CNY")
		}
	}

	for k := range series {
		sort.Slice(series[k], func(i, j int) bool { return series[k][i].FY < series[k][j].FY })
	}

	if rev := series["Revenue"]; len(rev) > 1 {
		yoy := make([]FactPoint, 0, len(rev)-1)
		for i := 1; i < len(rev); i++ {
			prev := rev[i-1]
			cur := rev[i]
			if prev.Value == 0 {
				continue
			}
			yoy = append(yoy, FactPoint{
				FY:    cur.FY,
				Value: (cur.Value - prev.Value) / prev.Value,
				Unit:  "ratio",
			})
		}
		if len(yoy) > 0 {
			series["RevenueYoY"] = yoy
		}
	}

	return series, "https://tushare.pro/document/2", nil
}

func getCNFinanceFromLocal(code string, years []int) (map[string][]FactPoint, string, error) {
	dataDir := getenv("DATA_DIR", "../data")
	p := filepath.Join(dataDir, "cn_finance_data.json")
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, "", err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, "", err
	}
	recRaw, ok := raw[code]
	if !ok {
		return nil, "", fmt.Errorf("local data not found for %s", code)
	}
	var rec CNFinanceCompany
	if err := json.Unmarshal(recRaw, &rec); err != nil {
		return nil, "", err
	}

	yearSet := map[int]bool{}
	for _, y := range years {
		yearSet[y] = true
	}

	series := map[string][]FactPoint{}
	appendMetric := func(metric string, year int, value *float64, unit string) {
		if value == nil {
			return
		}
		if len(yearSet) > 0 && !yearSet[year] {
			return
		}
		series[metric] = append(series[metric], FactPoint{
			FY:    year,
			Value: *value,
			Unit:  unit,
		})
	}

	for _, row := range rec.Data {
		unit := row.Unit
		if unit == "" {
			unit = "CNY"
		}
		appendMetric("Revenue", row.Year, row.Revenue, unit)
		appendMetric("GrossProfit", row.Year, row.GrossProfit, unit)
		appendMetric("OperatingIncome", row.Year, row.OperatingIncome, unit)
		appendMetric("NetIncome", row.Year, row.NetIncome, unit)
		appendMetric("RAndD", row.Year, row.RAndD, unit)
		appendMetric("TotalAssets", row.Year, row.TotalAssets, unit)
		appendMetric("TotalLiabilities", row.Year, row.TotalLiabilities, unit)
	}

	for k := range series {
		sort.Slice(series[k], func(i, j int) bool { return series[k][i].FY < series[k][j].FY })
	}

	if rev := series["Revenue"]; len(rev) > 1 {
		yoy := make([]FactPoint, 0, len(rev)-1)
		for i := 1; i < len(rev); i++ {
			prev := rev[i-1]
			cur := rev[i]
			if prev.Value == 0 {
				continue
			}
			yoy = append(yoy, FactPoint{
				FY:    cur.FY,
				Value: (cur.Value - prev.Value) / prev.Value,
				Unit:  "ratio",
			})
		}
		if len(yoy) > 0 {
			series["RevenueYoY"] = yoy
		}
	}

	return series, "local:data/cn_finance_data.json#" + code, nil
}

func callTushare(apiURL, token, apiName string, params map[string]any) ([]map[string]any, error) {
	body, _ := json.Marshal(map[string]any{
		"api_name": apiName,
		"token":    token,
		"params":   params,
	})
	req, _ := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := glmHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tushare status=%d body=%s", resp.StatusCode, string(respBody))
	}

	var out TushareResp
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, err
	}
	if out.Code != 0 {
		return nil, fmt.Errorf("tushare code=%d msg=%s", out.Code, out.Msg)
	}
	rows := make([]map[string]any, 0, len(out.Data.Items))
	for _, item := range out.Data.Items {
		switch row := item.(type) {
		case map[string]any:
			rows = append(rows, row)
		case []any:
			m := map[string]any{}
			for i, field := range out.Data.Fields {
				if i < len(row) {
					m[field] = row[i]
				}
			}
			rows = append(rows, m)
		}
	}
	return rows, nil
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprintf("%v", v)
	}
}

func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func seriesToText(series map[string][]FactPoint) string {
	keys := make([]string, 0, len(series))
	for k := range series {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := []string{}
	for _, k := range keys {
		arr := series[k]
		if len(arr) == 0 {
			continue
		}
		parts := []string{}
		for _, p := range arr {
			parts = append(parts, fmt.Sprintf("%d:%s", p.FY, formatFloat(p.Value)))
		}
		lines = append(lines, fmt.Sprintf("%s: %s", k, strings.Join(parts, " ")))
	}
	return strings.Join(lines, "\n")
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func metricLabel(metric string) string {
	switch metric {
	case "Revenue":
		return "营收"
	case "GrossProfit":
		return "毛利"
	case "OperatingIncome":
		return "营业利润"
	case "NetIncome":
		return "净利润"
	case "RAndD":
		return "研发投入"
	case "TotalAssets":
		return "总资产"
	case "TotalLiabilities":
		return "总负债"
	case "RevenueYoY":
		return "营收同比"
	default:
		return metric
	}
}

func formatMetricValue(metric string, value float64) string {
	if metric == "RevenueYoY" {
		return fmt.Sprintf("%.2f%%", value*100)
	}
	return formatFloat(value)
}

func truncateRunes(s string, max int) string {
	if max <= 0 || s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

func seriesYearRange(series map[string][]FactPoint) (int, int) {
	minY := 0
	maxY := 0
	for _, arr := range series {
		for _, p := range arr {
			if p.FY <= 0 {
				continue
			}
			if minY == 0 || p.FY < minY {
				minY = p.FY
			}
			if maxY == 0 || p.FY > maxY {
				maxY = p.FY
			}
		}
	}
	return minY, maxY
}

func maybeCompanyBrief(apiKey string, target CompareTarget, question string) string {
	name := strings.TrimSpace(target.Name)
	if apiKey == "" || name == "" {
		return ""
	}
	system := "你是中文财经助手。"
	user := fmt.Sprintf("请用一句话简要描述公司 %s(%s)，不超过40字，不要编造具体财务数字。用户问题：%s", name, strings.ToUpper(strings.TrimSpace(target.Symbol)), question)
	raw, err := callGLMRaw(apiKey, system, user, 0.2, 120)
	if err != nil {
		return ""
	}
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "`")
	raw = strings.ReplaceAll(raw, "\n", " ")
	raw = strings.ReplaceAll(raw, "\r", " ")
	raw = strings.TrimSpace(raw)
	return truncateRunes(raw, 60)
}

func buildFinanceAnswer(name, code, sourceTag string, series map[string][]FactPoint, brief string) string {
	if len(series) == 0 {
		return "未获取到可用的财务数据。"
	}
	keys := []string{"Revenue", "NetIncome", "OperatingIncome", "RevenueYoY", "TotalAssets", "TotalLiabilities"}
	lines := []string{}
	for _, k := range keys {
		arr := series[k]
		if len(arr) == 0 {
			continue
		}
		parts := make([]string, 0, len(arr))
		for _, p := range arr {
			parts = append(parts, fmt.Sprintf("%d:%s", p.FY, formatMetricValue(k, p.Value)))
		}
		lines = append(lines, fmt.Sprintf("- %s：%s", metricLabel(k), strings.Join(parts, "；")))
	}
	if len(lines) == 0 {
		return fmt.Sprintf("%s(%s) 已查询到数据，但关键指标为空。", name, code)
	}

	sourceText := "AkShare"
	if sourceTag == "eodhd" {
		sourceText = "EODHD"
	}
	if sourceTag == "local_fallback" {
		sourceText = "本地回退数据"
	}
	yearText := ""
	if minY, maxY := seriesYearRange(series); minY > 0 {
		if minY == maxY {
			yearText = fmt.Sprintf("%d年", minY)
		} else {
			yearText = fmt.Sprintf("%d-%d年", minY, maxY)
		}
	}
	intro := fmt.Sprintf("已识别为 %s(%s)，以下为%s财务指标概览（来源：%s）。", name, code, yearText, sourceText)
	if strings.TrimSpace(brief) != "" {
		intro += "\n公司简介：" + strings.TrimSpace(brief)
	}
	return fmt.Sprintf(
		"%s\n%s\n\n已基于同一批数据生成图表和表格，可直接核验。",
		intro,
		strings.Join(lines, "\n"),
	)
}

func buildEvidenceFallbackAnswer(evidence string) string {
	return "模型服务当前不可用，以下是基于已查询数据的直接结果：\n\n" + evidence + "\n\n图表和表格已按查询结果生成。"
}

func buildRevenueChart(rev []FactPoint) map[string]any {
	points := make([]map[string]any, 0, len(rev))
	for _, p := range rev {
		points = append(points, map[string]any{
			"x": p.FY,
			"y": p.Value,
		})
	}
	return map[string]any{
		"title":  "营收趋势",
		"type":   "line",
		"xLabel": "年度",
		"yLabel": "营收",
		"series": []map[string]any{
			{
				"name":   "营收",
				"points": points,
			},
		},
	}
}

func buildBestChartFromSeriesOld(series map[string][]FactPoint) any {
	if len(series) == 0 {
		return nil
	}
	metric := "Revenue"
	if len(series[metric]) == 0 {
		keys := make([]string, 0, len(series))
		for k := range series {
			if len(series[k]) > 0 {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			return nil
		}
		metric = keys[0]
	}
	arr := series[metric]
	points := make([]map[string]any, 0, len(arr))
	for _, p := range arr {
		points = append(points, map[string]any{"x": p.FY, "y": p.Value})
	}
	return map[string]any{
		"title":  metric + " 趋势",
		"type":   "line",
		"xLabel": "年度",
		"yLabel": metric,
		"series": []map[string]any{
			{
				"name":   metric,
				"points": points,
			},
		},
	}
}

func buildBestChartFromSeries(series map[string][]FactPoint, name string) any {
	if len(series) == 0 {
		return nil
	}
	metric := "Revenue"
	if len(series[metric]) == 0 {
		keys := make([]string, 0, len(series))
		for k := range series {
			if len(series[k]) > 0 {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			return nil
		}
		metric = keys[0]
	}
	arr := series[metric]
	points := make([]map[string]any, 0, len(arr))
	for _, p := range arr {
		points = append(points, map[string]any{"x": p.FY, "y": p.Value})
	}
	seriesName := metricLabel(metric)
	if strings.TrimSpace(name) != "" {
		seriesName = strings.TrimSpace(name)
	}
	title := metricLabel(metric) + "趋势"
	if strings.TrimSpace(name) != "" {
		title = strings.TrimSpace(name) + " · " + metricLabel(metric) + "趋势"
	}
	return map[string]any{
		"title":  title,
		"type":   "line",
		"xLabel": "年度",
		"yLabel": metricLabel(metric),
		"series": []map[string]any{
			{
				"name":   seriesName,
				"points": points,
			},
		},
	}
}

func buildTableFromSeries(name, code string, series map[string][]FactPoint) any {
	if len(series) == 0 {
		return nil
	}
	yearSet := map[int]bool{}
	for _, arr := range series {
		for _, p := range arr {
			yearSet[p.FY] = true
		}
	}
	years := make([]int, 0, len(yearSet))
	for y := range yearSet {
		years = append(years, y)
	}
	sort.Ints(years)
	if len(years) == 0 {
		return nil
	}

	keys := make([]string, 0, len(series))
	for k := range series {
		if len(series[k]) > 0 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	index := map[string]map[int]float64{}
	for _, k := range keys {
		index[k] = map[int]float64{}
		for _, p := range series[k] {
			index[k][p.FY] = p.Value
		}
	}

	columns := []string{"年度"}
	for _, k := range keys {
		columns = append(columns, metricLabel(k))
	}
	rows := make([][]any, 0, len(years))
	for _, y := range years {
		row := []any{y}
		for _, k := range keys {
			if v, ok := index[k][y]; ok {
				row = append(row, v)
			} else {
				row = append(row, "")
			}
		}
		rows = append(rows, row)
	}

	title := "AkShare 财务数据表"
	if strings.TrimSpace(name) != "" || strings.TrimSpace(code) != "" {
		title = fmt.Sprintf("AkShare 财务数据表 - %s(%s)", strings.TrimSpace(name), strings.TrimSpace(code))
	}
	return map[string]any{
		"title":   title,
		"columns": columns,
		"rows":    rows,
	}
}

func buildAkshareTraceURL(code string, years []int) string {
	base := "/api/source/akshare"
	yearParts := make([]string, 0, len(years))
	for _, y := range years {
		yearParts = append(yearParts, strconv.Itoa(y))
	}
	q := url.Values{}
	q.Set("code", code)
	if len(yearParts) > 0 {
		q.Set("years", strings.Join(yearParts, ","))
	}
	return base + "?" + q.Encode()
}

func handleTushareSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeTraceHTML(w, "请求错误", "仅支持 GET 请求。")
		return
	}
	mode := strings.TrimSpace(r.URL.Query().Get("mode"))
	if mode == "board" {
		year, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("year")))
		month, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("month")))
		if year <= 0 || month <= 0 {
			year, month = extractYearMonth("")
		}
		items, _, err := getBoardPerformanceFromAkshare(year, month)
		if err != nil {
			writeTraceHTML(w, "AkShare 板块查询记录", fmt.Sprintf(
				"<div>查询失败：%s</div><div>年份：%d</div><div>月份：%d</div>",
				htmlEsc(sanitizeAkshareError(err.Error())),
				year,
				month,
			))
			return
		}
		content := "<!doctype html><html><head><meta charset=\"utf-8\"/><title>AkShare 板块查询记录</title></head><body style=\"font-family:Arial;padding:20px;\">"
		content += fmt.Sprintf("<h2>AkShare 板块查询记录（%d-%02d）</h2>", year, month)
		content += "<div><a target=\"_blank\" href=\"https://akshare.akfamily.xyz/data/stock/stock.html\">AkShare 官方文档</a></div>"
		content += "<table border=\"1\" cellpadding=\"6\" cellspacing=\"0\" style=\"margin-top:12px;border-collapse:collapse;\">"
		content += "<tr><th>Rank</th><th>板块</th><th>代码</th><th>涨跌幅</th><th>起始交易日</th><th>结束交易日</th><th>起始收盘</th><th>结束收盘</th></tr>"
		for i, it := range items {
			content += "<tr>"
			content += "<td>" + htmlEsc(fmt.Sprintf("%d", i+1)) + "</td>"
			content += "<td>" + htmlEsc(it.Name) + "</td>"
			content += "<td>" + htmlEsc(it.Code) + "</td>"
			content += "<td>" + htmlEsc(fmt.Sprintf("%.2f%%", it.PctChange)) + "</td>"
			content += "<td>" + htmlEsc(it.StartDate) + "</td>"
			content += "<td>" + htmlEsc(it.EndDate) + "</td>"
			content += "<td>" + htmlEsc(fmt.Sprintf("%.4f", it.StartClose)) + "</td>"
			content += "<td>" + htmlEsc(fmt.Sprintf("%.4f", it.EndClose)) + "</td>"
			content += "</tr>"
		}
		content += "</table></body></html>"
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(content))
		return
	}
	if mode == "stock_rank" {
		limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
		windowDays, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("window")))
		if limit <= 0 {
			limit = 10
		}
		if windowDays <= 0 {
			windowDays = 1
		}
		items, _, periodLabel, err := getStockRankFromAkshare(limit, windowDays)
		if err != nil {
			writeTraceHTML(w, "AkShare 个股排行查询记录", fmt.Sprintf(
				"<div>查询失败：%s</div><div>Top：%d</div><div>窗口天数：%d</div>",
				htmlEsc(sanitizeAkshareError(err.Error())),
				limit,
				windowDays,
			))
			return
		}
		content := "<!doctype html><html><head><meta charset=\"utf-8\"/><title>AkShare 个股排行查询记录</title></head><body style=\"font-family:Arial;padding:20px;\">"
		content += "<h2>AkShare 个股排行查询记录</h2>"
		content += "<div><b>时间口径：</b>" + htmlEsc(periodLabel) + "</div>"
		content += "<div><b>TopN：</b>" + htmlEsc(strconv.Itoa(limit)) + "</div>"
		content += "<div style=\"margin-top:8px;\"><a target=\"_blank\" href=\"https://akshare.akfamily.xyz/data/stock/stock.html\">AkShare 官方文档</a></div>"
		content += "<table border=\"1\" cellpadding=\"6\" cellspacing=\"0\" style=\"margin-top:12px;border-collapse:collapse;\">"
		content += "<tr><th>Rank</th><th>股票</th><th>代码</th><th>涨跌幅</th><th>最新价</th><th>成交额</th></tr>"
		for i, it := range items {
			content += "<tr>"
			content += "<td>" + htmlEsc(fmt.Sprintf("%d", i+1)) + "</td>"
			content += "<td>" + htmlEsc(it.Name) + "</td>"
			content += "<td>" + htmlEsc(it.Code) + "</td>"
			content += "<td>" + htmlEsc(fmt.Sprintf("%.2f%%", it.PctChange)) + "</td>"
			if it.Latest != nil {
				content += "<td>" + htmlEsc(fmt.Sprintf("%.2f", *it.Latest)) + "</td>"
			} else {
				content += "<td>-</td>"
			}
			if it.Turnover != nil {
				content += "<td>" + htmlEsc(fmt.Sprintf("%.0f", *it.Turnover)) + "</td>"
			} else {
				content += "<td>-</td>"
			}
			content += "</tr>"
		}
		content += "</table></body></html>"
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(content))
		return
	}

	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		writeTraceHTML(w, "AkShare 查询记录", "缺少股票代码参数（code）。")
		return
	}
	years := parseYearsCSV(r.URL.Query().Get("years"))
	series, _, err := getCNFinanceFromAkshare(code, years)
	if err != nil {
		writeTraceHTML(w, "AkShare 查询记录", fmt.Sprintf(
			"<div>查询失败：%s</div><div>股票代码：%s</div><div>年份过滤：%s</div>",
			htmlEsc(sanitizeAkshareError(err.Error())),
			htmlEsc(code),
			htmlEsc(r.URL.Query().Get("years")),
		))
		return
	}
	table := buildTableFromSeries("", code, series)

	content := "<!doctype html><html><head><meta charset=\"utf-8\"/><title>AkShare 查询记录</title></head><body style=\"font-family:Arial;padding:20px;\">"
	content += "<h2>AkShare 查询记录</h2>"
	content += "<div><b>股票代码：</b>" + htmlEsc(code) + "</div>"
	content += "<div><b>年份过滤：</b>" + htmlEsc(r.URL.Query().Get("years")) + "</div>"
	content += "<div style=\"margin-top:8px;\"><a target=\"_blank\" href=\"https://akshare.akfamily.xyz/data/stock/stock.html\">AkShare 官方文档</a></div>"
	content += "<pre style=\"background:#f8fafc;border:1px solid #e2e8f0;padding:12px;border-radius:8px;white-space:pre-wrap;\">" + htmlEsc(seriesToText(series)) + "</pre>"
	if table != nil {
		if tb, ok := table.(map[string]any); ok {
			cols, _ := tb["columns"].([]string)
			rows, _ := tb["rows"].([][]any)
			content += "<h3>结构化表格</h3><table border=\"1\" cellpadding=\"6\" cellspacing=\"0\" style=\"border-collapse:collapse;\">"
			content += "<tr>"
			for _, c := range cols {
				content += "<th>" + htmlEsc(fmt.Sprintf("%v", c)) + "</th>"
			}
			content += "</tr>"
			for _, row := range rows {
				content += "<tr>"
				for _, cell := range row {
					content += "<td>" + htmlEsc(fmt.Sprintf("%v", cell)) + "</td>"
				}
				content += "</tr>"
			}
			content += "</table>"
		}
	}
	content += "</body></html>"

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(content))
}

func handleEODHDSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeTraceHTML(w, "请求错误", "仅支持 GET 请求。")
		return
	}
	symbol := strings.TrimSpace(r.URL.Query().Get("symbol"))
	if symbol == "" {
		writeTraceHTML(w, "EODHD 查询记录", "缺少股票代码参数（symbol）。")
		return
	}
	years := parseYearsCSV(r.URL.Query().Get("years"))
	series, _, err := getUSFinanceFromEODHD(symbol, years)
	if err != nil {
		writeTraceHTML(w, "EODHD 查询记录", fmt.Sprintf(
			"<div>查询失败：%s</div><div>股票代码：%s</div><div>年份过滤：%s</div>",
			htmlEsc(sanitizeEODHDError(err.Error())),
			htmlEsc(strings.ToUpper(symbol)),
			htmlEsc(r.URL.Query().Get("years")),
		))
		return
	}
	table := buildTableFromSeries("", strings.ToUpper(symbol), series)

	content := "<!doctype html><html><head><meta charset=\"utf-8\"/><title>EODHD 查询记录</title></head><body style=\"font-family:Arial;padding:20px;\">"
	content += "<h2>EODHD 查询记录（美股接口）</h2>"
	content += "<div><b>股票代码：</b>" + htmlEsc(strings.ToUpper(symbol)) + "</div>"
	content += "<div><b>年份过滤：</b>" + htmlEsc(r.URL.Query().Get("years")) + "</div>"
	content += "<div style=\"margin-top:8px;\"><a target=\"_blank\" href=\"https://eodhd.com/financial-apis/\">EODHD 官方文档</a></div>"
	content += "<pre style=\"background:#f8fafc;border:1px solid #e2e8f0;padding:12px;border-radius:8px;white-space:pre-wrap;\">" + htmlEsc(seriesToText(series)) + "</pre>"
	if table != nil {
		if tb, ok := table.(map[string]any); ok {
			cols, _ := tb["columns"].([]string)
			rows, _ := tb["rows"].([][]any)
			content += "<h3>结构化表格</h3><table border=\"1\" cellpadding=\"6\" cellspacing=\"0\" style=\"border-collapse:collapse;\">"
			content += "<tr>"
			for _, c := range cols {
				content += "<th>" + htmlEsc(fmt.Sprintf("%v", c)) + "</th>"
			}
			content += "</tr>"
			for _, row := range rows {
				content += "<tr>"
				for _, cell := range row {
					content += "<td>" + htmlEsc(fmt.Sprintf("%v", cell)) + "</td>"
				}
				content += "</tr>"
			}
			content += "</table>"
		}
	}
	content += "</body></html>"

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(content))
}

func handleMarketHotspots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
		return
	}
	limit := 8
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 20 {
		limit = 20
	}
	if limit < 1 {
		limit = 1
	}

	items, traceURL, err := getMarketHotspotsFromValueCell(limit)
	if err != nil {
		writeJSON(w, 502, map[string]any{
			"error":     err.Error(),
			"source":    "valuecell",
			"items":     []MarketHotspot{},
			"traceUrl":  traceURL,
			"updatedAt": time.Now().UTC().Format(time.RFC3339),
		})
		return
	}
	writeJSON(w, 200, map[string]any{
		"source":    "valuecell",
		"items":     items,
		"traceUrl":  traceURL,
		"updatedAt": time.Now().UTC().Format(time.RFC3339),
	})
}

func handleValueCellSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeTraceHTML(w, "请求错误", "仅支持 GET 请求。")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	companies := strings.TrimSpace(r.URL.Query().Get("companies"))
	mode := strings.TrimSpace(r.URL.Query().Get("mode"))
	apiURL := strings.TrimSpace(os.Getenv("VALUECELL_API_URL"))
	if apiURL == "" {
		apiURL = "http://127.0.0.1:8010/api/v1"
	}
	agentName := strings.TrimSpace(os.Getenv("VALUECELL_AGENT_NAME"))
	if agentName == "" {
		agentName = "自动路由（未指定）"
	}

	content := "<div><b>模式：</b>ValueCell 深度分析</div>"
	if mode != "" {
		content += "<div><b>子模式：</b>" + htmlEsc(mode) + "</div>"
	}
	content += "<div><b>问题：</b>" + htmlEsc(query) + "</div>"
	content += "<div><b>公司：</b>" + htmlEsc(companies) + "</div>"
	if strings.EqualFold(mode, "hotspots_crawl") {
		content += "<div><b>抓取源：</b><a target=\"_blank\" href=\"" + htmlEsc(query) + "\">" + htmlEsc(query) + "</a></div>"
	} else {
		content += "<div><b>接口：</b>" + htmlEsc(apiURL+"/agents/stream") + "</div>"
	}
	content += "<div><b>Agent：</b>" + htmlEsc(agentName) + "</div>"
	content += "<div style=\"margin-top:10px;\"><a target=\"_blank\" href=\"https://github.com/ValueCell-ai/valuecell\">ValueCell 官方仓库</a></div>"
	writeTraceHTML(w, "ValueCell 深度分析查询记录", content)
}

func buildValueCellTraceURL(query string, companies []string) string {
	base := "/api/source/valuecell"
	q := url.Values{}
	q.Set("query", query)
	if len(companies) > 0 {
		q.Set("companies", strings.Join(companies, ", "))
	}
	return base + "?" + q.Encode()
}

func deriveGLMCompatibleBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "https://open.bigmodel.cn/api/paas/v4"
	}
	u, err := url.Parse(raw)
	if err != nil || strings.TrimSpace(u.Scheme) == "" || strings.TrimSpace(u.Host) == "" {
		return "https://open.bigmodel.cn/api/paas/v4"
	}
	path := strings.TrimRight(u.Path, "/")
	path = strings.TrimSuffix(path, "/chat/completions")
	path = strings.TrimRight(path, "/")
	if path == "" {
		path = "/api/paas/v4"
	}
	u.Path = path
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/")
}

func callValueCellConfigJSON(method, endpoint string, payload any) error {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(method, endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("valuecell config status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func bootstrapValueCellProviderFromGLM() {
	apiRoot := strings.TrimSpace(os.Getenv("VALUECELL_API_URL"))
	if apiRoot == "" {
		apiRoot = "http://127.0.0.1:8010/api/v1"
	}
	apiRoot = strings.TrimRight(apiRoot, "/")
	if !strings.HasSuffix(strings.ToLower(apiRoot), "/api/v1") {
		apiRoot = strings.TrimRight(apiRoot, "/") + "/api/v1"
	}
	glmKey := strings.TrimSpace(os.Getenv("GLM_KEY"))
	if glmKey == "" {
		return
	}
	modelID := strings.TrimSpace(os.Getenv("LLM_MODEL"))
	if modelID == "" {
		modelID = "glm-4-flash"
	}
	baseURL := deriveGLMCompatibleBaseURL(getenv("GLM_API_URL", glmURL))

	if err := callValueCellConfigJSON(http.MethodPut, apiRoot+"/models/providers/default", map[string]any{
		"provider": "openai-compatible",
	}); err != nil {
		log.Printf("[valuecell-bootstrap] set default provider failed: %v", err)
	}
	if err := callValueCellConfigJSON(http.MethodPut, apiRoot+"/models/providers/openai-compatible/config", map[string]any{
		"api_key":  glmKey,
		"base_url": baseURL,
	}); err != nil {
		log.Printf("[valuecell-bootstrap] update provider config failed: %v", err)
	}
	if err := callValueCellConfigJSON(http.MethodPut, apiRoot+"/models/providers/openai-compatible/default-model", map[string]any{
		"model_id":   modelID,
		"model_name": modelID,
	}); err != nil {
		log.Printf("[valuecell-bootstrap] set default model failed: %v", err)
	}
}

func getDeepAnalysisFromValueCell(query string, companies []string) (string, string, error) {
	bootstrapValueCellProviderFromGLM()
	callBridge := func(agentName string) ([]byte, error) {
		args := []string{"--query", query}
		apiURL := strings.TrimSpace(os.Getenv("VALUECELL_API_URL"))
		if apiURL == "" {
			apiURL = "http://127.0.0.1:8010/api/v1"
		}
		args = append(args, "--api-url", apiURL)
		if len(companies) > 0 {
			args = append(args, "--companies", strings.Join(companies, ","))
		}
		if strings.TrimSpace(agentName) != "" {
			args = append(args, "--agent-name", strings.TrimSpace(agentName))
		}
		return runValueCellBridge(args...)
	}

	agentName := strings.TrimSpace(os.Getenv("VALUECELL_AGENT_NAME"))
	preferredAgent := agentName
	if preferredAgent == "" {
		// 当前 ValueCell 在本地默认自动路由稳定性不足，优先使用可用的 NewsAgent。
		preferredAgent = "NewsAgent"
	}
	out, err := callBridge(preferredAgent)
	if err != nil && preferredAgent != "" {
		// 指定 agent 失败时再回退自动路由，防止单点失败。
		out, err = callBridge("")
	}
	if err != nil {
		return "", "https://github.com/ValueCell-ai/valuecell", err
	}
	sourceURL := "https://github.com/ValueCell-ai/valuecell"
	var resp struct {
		AnalysisText string   `json:"analysis_text"`
		ToolEvents   []string `json:"tool_events"`
		SourceURL    string   `json:"source_url"`
		Error        string   `json:"error"`
	}
	if e := json.Unmarshal(out, &resp); e != nil {
		return "", sourceURL, e
	}
	if strings.TrimSpace(resp.SourceURL) != "" {
		sourceURL = strings.TrimSpace(resp.SourceURL)
	}
	if strings.TrimSpace(resp.Error) != "" {
		errText := strings.ToLower(strings.TrimSpace(resp.Error))
		shouldRetryNews := preferredAgent == "" &&
			(strings.Contains(errText, "unsupported agent") ||
				strings.Contains(errText, "resolve agent card") ||
				strings.Contains(errText, "error executing task") ||
				strings.Contains(errText, "planner"))
		if shouldRetryNews {
			out2, err2 := callBridge("NewsAgent")
			if err2 == nil {
				if e2 := json.Unmarshal(out2, &resp); e2 == nil && strings.TrimSpace(resp.Error) == "" {
					goto CONTINUE_PARSE
				}
			}
		}
		return "", sourceURL, fmt.Errorf("%s", strings.TrimSpace(resp.Error))
	}
CONTINUE_PARSE:
	analysis := strings.TrimSpace(resp.AnalysisText)
	if analysis == "" {
		return "", sourceURL, fmt.Errorf("valuecell returned empty analysis")
	}
	if len(resp.ToolEvents) > 0 {
		analysis += "\n\n[ValueCell 工具调用]\n- " + strings.Join(resp.ToolEvents, "\n- ")
	}
	return analysis, sourceURL, nil
}

func getMarketHotspotsFromValueCell(limit int) ([]MarketHotspot, string, error) {
	bootstrapValueCellProviderFromGLM()
	if limit <= 0 {
		limit = 8
	}
	if limit > 20 {
		limit = 20
	}
	lang := strings.TrimSpace(os.Getenv("VALUECELL_HOTSPOT_LANG"))
	if lang == "" {
		lang = "zh"
	}

	if items, traceURL, err := getMarketHotspotsFromCrawler(limit, lang); err == nil && len(items) > 0 {
		return items, traceURL, nil
	}

	items, traceURL, err := getMarketHotspotsFromValueCellAgent(limit)
	if err != nil {
		return nil, traceURL, err
	}
	return items, traceURL, nil
}

func getMarketHotspotsFromCrawler(limit int, language string) ([]MarketHotspot, string, error) {
	lang := strings.TrimSpace(language)
	if lang == "" {
		lang = "zh"
	}
	sourceURL := "https://valuecell.ai/api/v1/leaderboard/?language=" + url.QueryEscape(lang)
	traceURL := "/api/source/valuecell?mode=hotspots_crawl&query=" + url.QueryEscape(sourceURL)
	out, err := runValueCellHotspotCrawler("--limit", strconv.Itoa(limit), "--language", lang)
	if err != nil {
		return nil, traceURL, err
	}
	var resp struct {
		Items     []MarketHotspot `json:"items"`
		SourceURL string          `json:"source_url"`
		Error     string          `json:"error"`
	}
	if e := json.Unmarshal(out, &resp); e != nil {
		return nil, traceURL, e
	}
	if strings.TrimSpace(resp.Error) != "" {
		return nil, traceURL, fmt.Errorf("%s", strings.TrimSpace(resp.Error))
	}
	if strings.TrimSpace(resp.SourceURL) != "" {
		sourceURL = strings.TrimSpace(resp.SourceURL)
		traceURL = "/api/source/valuecell?mode=hotspots_crawl&query=" + url.QueryEscape(sourceURL)
	}
	items := normalizeHotspotItems(resp.Items, limit)
	if len(items) == 0 {
		return nil, traceURL, fmt.Errorf("crawler returned empty hotspots")
	}
	return items, traceURL, nil
}

func getMarketHotspotsFromValueCellAgent(limit int) ([]MarketHotspot, string, error) {
	query := fmt.Sprintf(
		"请给出%d条“市场热点提问”，覆盖A股/港股/美股活跃方向。优先输出格式：category|title|symbol@@category|title|symbol。若无法输出该格式，再输出严格JSON：{\"items\":[{\"category\":\"个股探索|市场观察|行业研究|A股|港股|美股\",\"title\":\"具体可分析的问题\",\"symbol\":\"公司简称或板块简称\"}]}. 禁止模板句和占位词。",
		limit,
	)
	apiURL := strings.TrimSpace(os.Getenv("VALUECELL_API_URL"))
	if apiURL == "" {
		apiURL = "http://127.0.0.1:8010/api/v1"
	}
	agentName := strings.TrimSpace(os.Getenv("VALUECELL_HOTSPOT_AGENT"))
	if agentName == "" {
		agentName = "NewsAgent"
	}
	out, err := runValueCellBridge("--query", query, "--api-url", apiURL, "--agent-name", agentName)
	traceURL := "/api/source/valuecell?mode=hotspots&query=" + url.QueryEscape(query)
	if err != nil {
		return nil, traceURL, err
	}
	var resp struct {
		AnalysisText string   `json:"analysis_text"`
		ToolEvents   []string `json:"tool_events"`
		SourceURL    string   `json:"source_url"`
		Error        string   `json:"error"`
	}
	if e := json.Unmarshal(out, &resp); e != nil {
		return nil, traceURL, e
	}
	if strings.TrimSpace(resp.Error) != "" {
		return nil, traceURL, fmt.Errorf("%s", strings.TrimSpace(resp.Error))
	}
	text := strings.TrimSpace(resp.AnalysisText)
	items := parseMarketHotspotsFromText(text, limit)
	if len(items) == 0 {
		return nil, traceURL, fmt.Errorf("valuecell returned no structured hotspots")
	}
	items = normalizeHotspotItems(items, limit)
	return items, traceURL, nil
}

func normalizeHotspotItems(items []MarketHotspot, limit int) []MarketHotspot {
	now := time.Now()
	out := normalizeMarketHotspots(items, limit)
	if len(out) > limit {
		out = out[:limit]
	}
	for i := range out {
		if strings.TrimSpace(out[i].Category) == "" {
			out[i].Category = "市场观察"
		}
		if strings.TrimSpace(out[i].Symbol) == "" {
			out[i].Symbol = now.Format("01-02") + "热点"
		}
	}
	return out
}

func parseMarketHotspotsFromText(text string, limit int) []MarketHotspot {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	candidates := extractJSONCandidates(text)
	for _, cand := range candidates {
		if items := decodeMarketHotspotsJSON(cand); len(items) > 0 {
			return normalizeMarketHotspots(items, limit)
		}
	}
	if items := parseMarketHotspotsPipeFormat(text, limit); len(items) > 0 {
		return normalizeMarketHotspots(items, limit)
	}
	items := []MarketHotspot{}
	lines := strings.Split(text, "\n")
	for _, ln := range lines {
		line := strings.TrimSpace(ln)
		line = strings.TrimPrefix(line, "-")
		line = strings.TrimPrefix(line, "*")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "```" || strings.HasPrefix(strings.ToLower(line), "```json") {
			continue
		}
		if strings.Contains(strings.ToLower(line), "valuecell 工具调用") {
			continue
		}
		if parsed := decodeMarketHotspotsJSON(line); len(parsed) > 0 {
			items = appendBoundedHotspots(items, parsed, limit)
			if limit > 0 && len(items) >= limit {
				break
			}
			continue
		}
		item := MarketHotspot{
			Category: "市场观察",
			Title:    line,
		}
		if m := regexp.MustCompile(`[(（]([A-Za-z0-9._-]{2,20})[)）]`).FindStringSubmatch(line); len(m) == 2 {
			item.Symbol = strings.TrimSpace(m[1])
		}
		items = append(items, item)
		if len(items) >= limit {
			break
		}
	}
	return normalizeMarketHotspots(items, limit)
}

func appendBoundedHotspots(dst []MarketHotspot, src []MarketHotspot, limit int) []MarketHotspot {
	for _, one := range src {
		dst = append(dst, one)
		if limit > 0 && len(dst) >= limit {
			break
		}
	}
	return dst
}

func parseMarketHotspotsPipeFormat(text string, limit int) []MarketHotspot {
	raw := strings.TrimSpace(text)
	if raw == "" || !strings.Contains(raw, "|") {
		return nil
	}
	raw = strings.ReplaceAll(raw, "\n", "")
	raw = strings.ReplaceAll(raw, "分类|问题|标的", "")
	raw = strings.ReplaceAll(raw, "category|title|symbol", "")
	markers := []string{
		"个股探索|", "市场观察|", "行业研究|", "宏观观察|", "主题轮动|",
		"A股|", "港股|", "美股|", "a股|",
	}
	for _, mk := range markers {
		raw = strings.ReplaceAll(raw, mk, "@@"+mk)
	}
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "@@"))
	parts := strings.Split(raw, "@@")
	items := make([]MarketHotspot, 0, len(parts))
	for _, one := range parts {
		seg := strings.TrimSpace(one)
		if seg == "" {
			continue
		}
		p := strings.SplitN(seg, "|", 3)
		if len(p) < 3 {
			continue
		}
		items = append(items, MarketHotspot{
			Category: strings.TrimSpace(p[0]),
			Title:    strings.TrimSpace(p[1]),
			Symbol:   strings.TrimSpace(p[2]),
		})
		if limit > 0 && len(items) >= limit {
			break
		}
	}
	return items
}

func extractJSONCandidates(text string) []string {
	out := []string{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		for _, ex := range out {
			if ex == s {
				return
			}
		}
		out = append(out, s)
	}
	add(text)
	if i := strings.Index(text, "```json"); i >= 0 {
		rest := text[i+7:]
		if j := strings.Index(rest, "```"); j > 0 {
			add(rest[:j])
		}
	}
	if i := strings.Index(text, "["); i >= 0 {
		if j := strings.LastIndex(text, "]"); j > i {
			add(text[i : j+1])
		}
	}
	if i := strings.Index(text, "{"); i >= 0 {
		if j := strings.LastIndex(text, "}"); j > i {
			add(text[i : j+1])
		}
	}
	return out
}

func decodeMarketHotspotsJSON(raw string) []MarketHotspot {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var direct []MarketHotspot
	if err := json.Unmarshal([]byte(raw), &direct); err == nil && len(direct) > 0 {
		return direct
	}
	var wrapped struct {
		Items    []MarketHotspot `json:"items"`
		Hotspots []MarketHotspot `json:"hotspots"`
		Data     []MarketHotspot `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapped); err == nil {
		if len(wrapped.Items) > 0 {
			return wrapped.Items
		}
		if len(wrapped.Hotspots) > 0 {
			return wrapped.Hotspots
		}
		if len(wrapped.Data) > 0 {
			return wrapped.Data
		}
	}
	var generic []map[string]any
	if err := json.Unmarshal([]byte(raw), &generic); err == nil && len(generic) > 0 {
		items := make([]MarketHotspot, 0, len(generic))
		for _, row := range generic {
			items = append(items, MarketHotspot{
				Category: firstNonEmptyString(row, "category", "type", "tag", "label", "分类", "类别", "赛道"),
				Title:    firstNonEmptyString(row, "title", "question", "topic", "content", "问题", "题目", "主题"),
				Symbol:   firstNonEmptyString(row, "symbol", "ticker", "stock", "company", "标的", "代码", "公司", "股票"),
			})
		}
		return items
	}
	var genericWrap map[string]any
	if err := json.Unmarshal([]byte(raw), &genericWrap); err == nil {
		for _, k := range []string{"items", "hotspots", "data", "list"} {
			if arr, ok := genericWrap[k].([]any); ok && len(arr) > 0 {
				items := make([]MarketHotspot, 0, len(arr))
				for _, one := range arr {
					if row, ok := one.(map[string]any); ok {
						items = append(items, MarketHotspot{
							Category: firstNonEmptyString(row, "category", "type", "tag", "label", "分类", "类别", "赛道"),
							Title:    firstNonEmptyString(row, "title", "question", "topic", "content", "问题", "题目", "主题"),
							Symbol:   firstNonEmptyString(row, "symbol", "ticker", "stock", "company", "标的", "代码", "公司", "股票"),
						})
					}
				}
				return items
			}
		}
	}
	return nil
}

func firstNonEmptyString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			s := strings.TrimSpace(asString(v))
			if s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func normalizeMarketHotspots(items []MarketHotspot, limit int) []MarketHotspot {
	out := make([]MarketHotspot, 0, len(items))
	seen := map[string]bool{}
	normalizeCategory := func(raw string) string {
		c := strings.TrimSpace(strings.Trim(raw, `"'`))
		if c == "" {
			return "市场观察"
		}
		c = strings.ReplaceAll(c, "｜", "|")
		c = strings.ReplaceAll(c, "／", "/")
		parts := strings.FieldsFunc(c, func(r rune) bool {
			return r == '|' || r == '/' || r == '、' || r == ',' || r == '，'
		})
		if len(parts) > 0 {
			c = strings.TrimSpace(parts[0])
		}
		switch c {
		case "个股探索", "市场观察", "行业研究", "宏观观察", "主题轮动", "A股", "a股", "港股", "美股":
			if c == "a股" {
				return "A股"
			}
			return c
		}
		switch {
		case strings.Contains(c, "个股"):
			return "个股探索"
		case strings.Contains(c, "行业"):
			return "行业研究"
		case strings.Contains(c, "宏观"):
			return "宏观观察"
		case strings.Contains(c, "主题"):
			return "主题轮动"
		case strings.Contains(strings.ToLower(c), "a股") || strings.Contains(c, "A股"):
			return "A股"
		case strings.Contains(c, "港"):
			return "港股"
		case strings.Contains(c, "美"):
			return "美股"
		default:
			return "市场观察"
		}
	}
	for _, it := range items {
		title := strings.TrimSpace(strings.Trim(it.Title, `"'`))
		if title == "" {
			continue
		}
		lowerTitle := strings.ToLower(title)
		if strings.Contains(lowerTitle, "transparent proxy") ||
			strings.Contains(title, "可直接发问") ||
			strings.Contains(title, "占位") ||
			strings.Contains(title, "来源：") ||
			strings.Contains(title, "[来源") ||
			strings.Contains(title, "关联标的") {
			continue
		}
		category := normalizeCategory(it.Category)
		symbol := strings.TrimSpace(it.Symbol)
		key := strings.ToLower(category + "|" + title + "|" + symbol)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, MarketHotspot{
			Category: category,
			Title:    title,
			Symbol:   symbol,
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func runValueCellBridge(args ...string) ([]byte, error) {
	return runPythonBridgeScript("VALUECELL_BRIDGE_SCRIPT", "./valuecell_bridge.py", "valuecell bridge", args...)
}

func runValueCellHotspotCrawler(args ...string) ([]byte, error) {
	return runPythonBridgeScript("VALUECELL_HOTSPOT_SCRIPT", "./valuecell_hotspots_bridge.py", "valuecell hotspot crawler", args...)
}

func runPythonBridgeScript(scriptEnvKey, defaultScript, errorPrefix string, args ...string) ([]byte, error) {
	pythonBin := strings.TrimSpace(os.Getenv("PYTHON_BIN"))
	if pythonBin == "" {
		pythonBin = "python"
	}
	scriptPath := strings.TrimSpace(os.Getenv(scriptEnvKey))
	if scriptPath == "" {
		scriptPath = defaultScript
	}
	cmdArgs := append([]string{scriptPath}, args...)
	cmd := exec.Command(pythonBin, cmdArgs...)
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, kv := range env {
		key := strings.ToUpper(strings.SplitN(kv, "=", 2)[0])
		if key == "PYTHONUTF8" || key == "PYTHONIOENCODING" || key == "PYTHONWARNINGS" {
			continue
		}
		filtered = append(filtered, kv)
	}
	filtered = append(filtered, "PYTHONUTF8=1", "PYTHONIOENCODING=UTF-8", "PYTHONWARNINGS=ignore")
	cmd.Env = filtered
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			msg := strings.TrimSpace(string(ee.Stderr))
			if msg != "" {
				return nil, fmt.Errorf("%s error: %s", errorPrefix, msg)
			}
		}
		return nil, fmt.Errorf("%s error: %w", errorPrefix, err)
	}
	return out, nil
}

func isBoardAnalysisQuestion(message string) bool {
	if isStockRankQuestion(message) {
		return false
	}
	re := regexp.MustCompile(`A股|a股|板块|行业|概念|涨跌|涨幅|跌幅|轮动`)
	if !re.MatchString(message) {
		return false
	}
	return regexp.MustCompile(`分析|走势|统计|排行|排名|top|Top|月份|月`).MatchString(message)
}

func isStockRankQuestion(message string) bool {
	hasStock := regexp.MustCompile(`股票|个股|只股票|只股|A股|a股`).MatchString(message)
	if !hasStock {
		return false
	}
	hasRank := regexp.MustCompile(`排行|排名|榜|top|Top|TOP|最猛|最强|涨幅|跌幅`).MatchString(message)
	if !hasRank {
		return false
	}
	return true
}

func extractTopLimit(message string, defaultLimit, maxLimit int) int {
	limit := defaultLimit
	reTop := regexp.MustCompile(`(?i)top\s*(\d{1,3})`)
	if m := reTop.FindStringSubmatch(message); len(m) == 2 {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			limit = n
		}
	}
	reCN := regexp.MustCompile(`(\d{1,3})\s*只`)
	if m := reCN.FindStringSubmatch(message); len(m) == 2 {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			limit = n
		}
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	return limit
}

func extractWindowDays(message string) int {
	m := strings.ToLower(strings.TrimSpace(message))
	if regexp.MustCompile(`过去一周|近一周|最近一周|本周`).MatchString(message) {
		return 7
	}
	if regexp.MustCompile(`过去一月|近一月|最近一月|本月`).MatchString(message) {
		return 30
	}
	if mm := regexp.MustCompile(`近(\d{1,3})天`).FindStringSubmatch(m); len(mm) == 2 {
		if n, err := strconv.Atoi(mm[1]); err == nil && n > 0 {
			if n > 120 {
				return 120
			}
			return n
		}
	}
	if mm := regexp.MustCompile(`(\d{1,3})天`).FindStringSubmatch(m); len(mm) == 2 {
		if n, err := strconv.Atoi(mm[1]); err == nil && n > 0 {
			if n > 120 {
				return 120
			}
			return n
		}
	}
	return 1
}

func extractYearMonth(message string) (int, int) {
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	ym1 := regexp.MustCompile(`(20\d{2})年\s*(1[0-2]|0?[1-9])月`)
	if m := ym1.FindStringSubmatch(message); len(m) == 3 {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		return y, mo
	}
	ym2 := regexp.MustCompile(`(20\d{2})[-/](1[0-2]|0?[1-9])`)
	if m := ym2.FindStringSubmatch(message); len(m) == 3 {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		return y, mo
	}
	return year, month
}

func monthRange(year, month int) (startDate string, endDate string) {
	if month < 1 {
		month = 1
	}
	if month > 12 {
		month = 12
	}
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 1, -1)
	return start.Format("20060102"), end.Format("20060102")
}

func getBoardPerformanceFromAkshare(year, month int) ([]BoardPerfItem, string, error) {
	limit := getenv("AKSHARE_BOARD_LIMIT", "20")
	out, err := runAkshareBridge("--mode", "board", "--year", strconv.Itoa(year), "--month", strconv.Itoa(month), "--limit", limit)
	if err != nil {
		return nil, "https://akshare.akfamily.xyz/data/stock/stock.html", err
	}
	var resp struct {
		Items  []BoardPerfItem `json:"items"`
		Error  string          `json:"error"`
		Source string          `json:"source_url"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, "https://akshare.akfamily.xyz/data/stock/stock.html", err
	}
	if strings.TrimSpace(resp.Error) != "" {
		return nil, "https://akshare.akfamily.xyz/data/stock/stock.html", fmt.Errorf("%s", resp.Error)
	}
	if len(resp.Items) == 0 {
		return nil, "https://akshare.akfamily.xyz/data/stock/stock.html", fmt.Errorf("board items empty")
	}
	source := strings.TrimSpace(resp.Source)
	if source == "" {
		source = "https://akshare.akfamily.xyz/data/stock/stock.html"
	}
	return resp.Items, source, nil
}

func getStockRankFromAkshare(limit, windowDays int) ([]StockRankItem, string, string, error) {
	if limit <= 0 {
		limit = 10
	}
	if windowDays <= 0 {
		windowDays = 1
	}
	out, err := runAkshareBridge("--mode", "stock_rank", "--limit", strconv.Itoa(limit), "--window", strconv.Itoa(windowDays))
	if err != nil {
		return nil, "https://akshare.akfamily.xyz/data/stock/stock.html", "", err
	}
	var resp struct {
		Items       []StockRankItem `json:"items"`
		Error       string          `json:"error"`
		Source      string          `json:"source_url"`
		PeriodLabel string          `json:"period_label"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, "https://akshare.akfamily.xyz/data/stock/stock.html", "", err
	}
	if strings.TrimSpace(resp.Error) != "" {
		return nil, "https://akshare.akfamily.xyz/data/stock/stock.html", "", fmt.Errorf("%s", sanitizeAkshareError(resp.Error))
	}
	if len(resp.Items) == 0 {
		return nil, "https://akshare.akfamily.xyz/data/stock/stock.html", "", fmt.Errorf("stock rank items empty")
	}
	source := strings.TrimSpace(resp.Source)
	if source == "" {
		source = "https://akshare.akfamily.xyz/data/stock/stock.html"
	}
	periodLabel := strings.TrimSpace(resp.PeriodLabel)
	if periodLabel == "" {
		periodLabel = "最新交易日"
	}
	return resp.Items, source, periodLabel, nil
}

func sanitizeAkshareError(raw string) string {
	msg := strings.TrimSpace(raw)
	if msg == "" {
		return "akshare unknown error"
	}
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "proxyerror") || strings.Contains(lower, "unable to connect to proxy") {
		return "网络代理导致 AkShare 请求失败（请关闭系统代理/VPN后重试）"
	}
	if strings.Contains(lower, "max retries exceeded") {
		return "AkShare 数据源连接重试超限（请检查网络连通性）"
	}
	if strings.Contains(lower, "remote end closed connection") || strings.Contains(lower, "connection aborted") {
		return "AkShare 数据源连接被远端关闭（通常是网络策略或站点反爬拦截）"
	}
	if strings.Contains(lower, "decode value starting with character '<'") || strings.Contains(lower, "jsondecodeerror") {
		return "AkShare 返回了非预期页面（可能被网关/反爬拦截）"
	}
	if strings.Contains(lower, "nonetype") && strings.Contains(lower, "not subscriptable") {
		return "AkShare 返回空数据结构（该标的在当前接口可能不受支持）"
	}
	if strings.Contains(lower, "name or service not known") || strings.Contains(lower, "no such host") {
		return "AkShare 数据源域名解析失败（请检查DNS或网络）"
	}
	if strings.Contains(lower, "timed out") || strings.Contains(lower, "timeout") {
		return "AkShare 数据源访问超时（请稍后重试）"
	}
	return msg
}

func sanitizeEODHDError(raw string) string {
	msg := strings.TrimSpace(raw)
	if msg == "" {
		return "eodhd unknown error"
	}
	msg = regexp.MustCompile(`(?i)(api_token=)[^&\s"]+`).ReplaceAllString(msg, "${1}***")
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "status=401"):
		return "EODHD 鉴权失败（请检查 EODHD_API_KEY）"
	case strings.Contains(lower, "status=403"):
		return "EODHD 无权限访问该接口（请检查套餐或Key权限）"
	case strings.Contains(lower, "status=404"):
		return "EODHD 未找到对应标的（请检查代码格式，如 BABA 或 0700.HK）"
	case strings.Contains(lower, "no such host") || strings.Contains(lower, "name or service not known"):
		return "EODHD 域名解析失败（当前环境无法访问 eodhd.com）"
	case strings.Contains(lower, "timed out") || strings.Contains(lower, "timeout"):
		return "EODHD 请求超时（请稍后重试）"
	}
	return msg
}

func sanitizePipelineError(err error) string {
	if err == nil {
		return ""
	}
	raw := strings.TrimSpace(err.Error())
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "eodhd") || strings.Contains(lower, "api_token=") || strings.Contains(lower, "fundamentals") {
		return sanitizeEODHDError(raw)
	}
	return sanitizeAkshareError(raw)
}

func runAkshareBridge(args ...string) ([]byte, error) {
	pythonBin := strings.TrimSpace(os.Getenv("PYTHON_BIN"))
	if pythonBin == "" {
		pythonBin = "python"
	}
	scriptPath := strings.TrimSpace(os.Getenv("AKSHARE_BRIDGE_SCRIPT"))
	if scriptPath == "" {
		scriptPath = "./akshare_bridge.py"
	}
	cmdArgs := append([]string{scriptPath}, args...)
	cmd := exec.Command(pythonBin, cmdArgs...)

	// AkShare occasionally picks up bad proxy settings from host env.
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, kv := range env {
		key := strings.ToUpper(strings.SplitN(kv, "=", 2)[0])
		if key == "HTTP_PROXY" || key == "HTTPS_PROXY" || key == "ALL_PROXY" || key == "NO_PROXY" ||
			key == "PYTHONUTF8" || key == "PYTHONIOENCODING" {
			continue
		}
		filtered = append(filtered, kv)
	}
	filtered = append(filtered, "PYTHONUTF8=1", "PYTHONIOENCODING=UTF-8")
	cmd.Env = filtered

	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			msg := strings.TrimSpace(string(ee.Stderr))
			if msg != "" {
				return nil, fmt.Errorf("akshare bridge error: %s", msg)
			}
		}
		return nil, fmt.Errorf("akshare bridge error: %w", err)
	}
	return out, nil
}

func pickField(row map[string]any, keys []string) any {
	for _, k := range keys {
		if v, ok := row[k]; ok {
			return v
		}
	}
	return nil
}

func buildStockRankEvidenceText(items []StockRankItem, periodLabel string) string {
	limit := 10
	if len(items) < limit {
		limit = len(items)
	}
	lines := []string{
		fmt.Sprintf("【A股个股涨幅排行（%s）】", periodLabel),
	}
	for i := 0; i < limit; i++ {
		it := items[i]
		lines = append(lines, fmt.Sprintf("%d. %s(%s): %.2f%%", i+1, it.Name, it.Code, it.PctChange))
	}
	return strings.Join(lines, "\n")
}

func buildStockRankAnswer(items []StockRankItem, periodLabel string) string {
	if len(items) == 0 {
		return "未获取到可用的A股个股排行数据。"
	}
	topN := 10
	if len(items) < topN {
		topN = len(items)
	}
	lines := make([]string, 0, topN)
	for i := 0; i < topN; i++ {
		it := items[i]
		line := fmt.Sprintf("%d) %s（%s）：%.2f%%", i+1, it.Name, it.Code, it.PctChange)
		lines = append(lines, line)
	}
	return fmt.Sprintf(
		"A股个股涨幅Top%d（%s）\n\n%s\n\n图表与表格已按本次查询结果生成，可直接核验。",
		topN,
		periodLabel,
		strings.Join(lines, "\n"),
	)
}

func buildStockRankChart(items []StockRankItem, periodLabel string) any {
	if len(items) == 0 {
		return nil
	}
	limit := 10
	if len(items) < limit {
		limit = len(items)
	}
	points := make([]map[string]any, 0, limit)
	for i := 0; i < limit; i++ {
		it := items[i]
		points = append(points, map[string]any{
			"x": it.Name,
			"y": it.PctChange,
		})
	}
	return map[string]any{
		"title":  fmt.Sprintf("A股个股涨幅Top%d（%s）", limit, periodLabel),
		"type":   "bar",
		"xLabel": "股票",
		"yLabel": "涨跌幅(%)",
		"series": []map[string]any{
			{
				"name":   "涨跌幅(%)",
				"points": points,
			},
		},
	}
}

func buildStockRankTable(items []StockRankItem, periodLabel string) any {
	if len(items) == 0 {
		return nil
	}
	rows := make([][]any, 0, len(items))
	for i, it := range items {
		latest := any("-")
		if it.Latest != nil {
			latest = *it.Latest
		}
		turnover := any("-")
		if it.Turnover != nil {
			turnover = *it.Turnover
		}
		rows = append(rows, []any{
			i + 1,
			it.Name,
			it.Code,
			fmt.Sprintf("%.2f%%", it.PctChange),
			latest,
			turnover,
		})
	}
	return map[string]any{
		"title": fmt.Sprintf("A股个股涨幅排行（%s）", periodLabel),
		"columns": []string{
			"排名",
			"股票",
			"代码",
			"涨跌幅",
			"最新价",
			"成交额",
		},
		"rows": rows,
	}
}

func buildBoardEvidenceText(items []BoardPerfItem, year, month int) string {
	limit := 10
	if len(items) < limit {
		limit = len(items)
	}
	lines := []string{
		fmt.Sprintf("【A股板块涨跌数据（%d年%d月）】", year, month),
		"按区间首尾收盘价计算涨跌幅：",
	}
	for i := 0; i < limit; i++ {
		it := items[i]
		lines = append(lines, fmt.Sprintf("%d. %s(%s): %.2f%%", i+1, it.Name, it.Code, it.PctChange))
	}
	return strings.Join(lines, "\n")
}

func buildBoardAnswer(items []BoardPerfItem, year, month int) string {
	if len(items) == 0 {
		return fmt.Sprintf("%d年%d月未获取到可用的A股板块数据。", year, month)
	}
	topN := 5
	if len(items) < topN {
		topN = len(items)
	}
	bottomN := 5
	if len(items) < bottomN {
		bottomN = len(items)
	}
	topLines := []string{}
	for i := 0; i < topN; i++ {
		it := items[i]
		topLines = append(topLines, fmt.Sprintf("%d) %s（%s）：%.2f%%", i+1, it.Name, it.Code, it.PctChange))
	}
	bottomLines := []string{}
	for i := len(items) - bottomN; i < len(items); i++ {
		it := items[i]
		bottomLines = append(bottomLines, fmt.Sprintf("%d) %s（%s）：%.2f%%", len(items)-i, it.Name, it.Code, it.PctChange))
	}
	return fmt.Sprintf(
		"%d年%d月A股板块涨跌分析（基于AkShare行业指数区间首尾收盘价）\n\n涨幅靠前：\n%s\n\n跌幅靠前：\n%s\n\n图表与表格已按本次查询结果生成，可直接核验。",
		year,
		month,
		strings.Join(topLines, "\n"),
		strings.Join(bottomLines, "\n"),
	)
}

func buildBoardChart(items []BoardPerfItem, year, month int) any {
	if len(items) == 0 {
		return nil
	}
	limit := 10
	if len(items) < limit {
		limit = len(items)
	}
	points := make([]map[string]any, 0, limit)
	for i := 0; i < limit; i++ {
		it := items[i]
		points = append(points, map[string]any{
			"x": it.Name,
			"y": it.PctChange,
		})
	}
	return map[string]any{
		"title":  fmt.Sprintf("%d年%d月 A股板块涨幅Top%d", year, month, limit),
		"type":   "bar",
		"xLabel": "板块",
		"yLabel": "涨跌幅(%)",
		"series": []map[string]any{
			{
				"name":   "涨跌幅(%)",
				"points": points,
			},
		},
	}
}

func buildBoardTable(items []BoardPerfItem, year, month int) any {
	if len(items) == 0 {
		return nil
	}
	rows := make([][]any, 0, len(items))
	for i, it := range items {
		rows = append(rows, []any{
			i + 1,
			it.Name,
			it.Code,
			fmt.Sprintf("%.2f%%", it.PctChange),
			it.StartDate,
			it.EndDate,
			it.StartClose,
			it.EndClose,
		})
	}
	return map[string]any{
		"title": fmt.Sprintf("%d年%d月 A股板块涨跌明细", year, month),
		"columns": []string{
			"排名",
			"板块",
			"代码",
			"涨跌幅",
			"起始交易日",
			"结束交易日",
			"起始收盘",
			"结束收盘",
		},
		"rows": rows,
	}
}

func buildBoardTraceURL(year, month int) string {
	base := "/api/source/akshare"
	q := url.Values{}
	q.Set("mode", "board")
	q.Set("year", strconv.Itoa(year))
	q.Set("month", strconv.Itoa(month))
	return base + "?" + q.Encode()
}

func buildDeepModeFailureAnswer(notes []string) string {
	msg := "深度分析暂不可用，已避免中断请求。"
	msg += "\n请检查 ValueCell 服务是否已启动，并确认 VALUECELL_API_URL / VALUECELL_AGENT_NAME 配置正确后重试。"
	if len(notes) > 0 {
		msg += "\n\n当前失败信息：\n- " + strings.Join(notes, "\n- ")
	}
	return msg
}

func weakValueCellResultReason(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return "返回内容为空"
	}
	lower := strings.ToLower(t)
	errorMarkers := []string{
		"planner is unavailable",
		"failed to initialize model/provider",
		"please configure a valid api key or provider settings",
		"failed to resolve agent card",
		"error executing task",
		"valuecell returned empty analysis",
		"valuecell endpoints unavailable",
	}
	for _, m := range errorMarkers {
		if strings.Contains(lower, m) {
			return "ValueCell 内部执行异常"
		}
	}
	noDataMarkers := []string{
		"无法访问具体的财务数据",
		"无法访问具体财务数据",
		"无法提供",
		"无法直接访问实时数据库",
		"通常，您可以通过以下途径获取",
		"you can obtain",
		"unable to access",
		"cannot access",
	}
	hasNoDataMarker := false
	for _, m := range noDataMarkers {
		if strings.Contains(lower, strings.ToLower(m)) {
			hasNoDataMarker = true
			break
		}
	}
	if !hasNoDataMarker {
		return ""
	}
	hasNumberEvidence := regexp.MustCompile(`\d{4}|\d+(\.\d+)?%|\d{2,}`).MatchString(t)
	if hasNumberEvidence {
		return ""
	}
	return "返回内容为通用说明，未提供可核验财务结果"
}

func buildStockRankTraceURL(limit, windowDays int) string {
	base := "/api/source/akshare"
	q := url.Values{}
	q.Set("mode", "stock_rank")
	q.Set("limit", strconv.Itoa(limit))
	q.Set("window", strconv.Itoa(windowDays))
	return base + "?" + q.Encode()
}

func getTushareToken() string {
	candidates := []string{
		"TUSHARE_TOKEN",
		"TUSHARE_API_KEY",
		"TUSHARE_KEY",
		"TS_TOKEN",
	}
	for _, k := range candidates {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func getTushareAPIURL() string {
	candidates := []string{
		"TUSHARE_API_URL",
		"TUSHARE_URL",
	}
	for _, k := range candidates {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}
