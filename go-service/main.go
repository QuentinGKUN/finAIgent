package main

import (
  "encoding/json"
  "fmt"
  "io"
  "log"
  "net/http"
  "os"
  "regexp"
  "sort"
  "strconv"
  "strings"
  "time"

  "github.com/PuerkitoBio/goquery"
)

type tickerEntry struct {
  CIK    int    `json:"cik_str"`
  Ticker string `json:"ticker"`
  Title  string `json:"title"`
}

type Company struct {
  Name    string   `json:"name"`
  CIK     string   `json:"cik"`
  Tickers []string `json:"tickers"`
}

var cacheLoaded bool
var tickers []tickerEntry

func main() {
  mux := http.NewServeMux()
  mux.HandleFunc("/sec/resolve", handleResolve)
  mux.HandleFunc("/sec/latest10k", handleLatest10K)
  mux.HandleFunc("/sec/companyfacts", handleCompanyFacts)

  port := getenv("GO_PORT", "3001")
  srv := &http.Server{
    Addr:              ":" + port,
    Handler:           withCORS(mux),
    ReadHeaderTimeout: 10 * time.Second,
  }
  log.Printf("Go service http://localhost:%s\n", port)
  log.Fatal(srv.ListenAndServe())
}

func getenv(k, d string) string {
  if v := os.Getenv(k); v != "" { return v }
  return d
}

func withCORS(next http.Handler) http.HandlerFunc {
  return func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Access-Control-Allow-Origin", "*")
    w.Header().Set("Access-Control-Allow-Headers", "Content-Type, User-Agent")
    w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
    if r.Method == http.MethodOptions { w.WriteHeader(204); return }
    next.ServeHTTP(w, r)
  }
}

func httpGET(u, ua string) ([]byte, error) {
  req, _ := http.NewRequest("GET", u, nil)
  req.Header.Set("User-Agent", ua)
  resp, err := http.DefaultClient.Do(req)
  if err != nil { return nil, err }
  defer resp.Body.Close()
  if resp.StatusCode != 200 {
    b, _ := io.ReadAll(resp.Body)
    return nil, fmt.Errorf("GET %s status=%d body=%s", u, resp.StatusCode, string(b))
  }
  return io.ReadAll(resp.Body)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
  w.Header().Set("Content-Type", "application/json; charset=utf-8")
  w.WriteHeader(code)
  _ = json.NewEncoder(w).Encode(v)
}

func loadTickers(ua string) error {
  if cacheLoaded { return nil }
  b, err := httpGET("https://www.sec.gov/files/company_tickers.json", ua)
  if err != nil { return err }
  var raw map[string]tickerEntry
  if err := json.Unmarshal(b, &raw); err != nil { return err }
  tickers = make([]tickerEntry, 0, len(raw))
  for _, v := range raw { tickers = append(tickers, v) }
  cacheLoaded = true
  return nil
}

func handleResolve(w http.ResponseWriter, r *http.Request) {
  q := strings.TrimSpace(r.URL.Query().Get("query"))
  ua := r.Header.Get("User-Agent")
  if ua == "" { ua = "FinAssistantChampion/1.0 (email: you@example.com)" }
  if q == "" { writeJSON(w, 400, map[string]any{"error":"missing query"}); return }

  if err := loadTickers(ua); err != nil { writeJSON(w, 500, map[string]any{"error":err.Error()}); return }

  qU := strings.ToUpper(q)
  bestScore := -1
  var best *tickerEntry

  for i := range tickers {
    e := tickers[i]
    score := 0
    if strings.ToUpper(e.Ticker) == qU { score += 100 }
    if strings.Contains(strings.ToUpper(e.Title), qU) { score += 50 }
    if strings.Contains(strings.ToUpper(e.Title), strings.ToUpper(q)) { score += 40 }
    if score > bestScore { bestScore = score; best = &e }
  }

  if best == nil || bestScore < 10 {
    writeJSON(w, 200, Company{Name:q, CIK:"", Tickers:[]string{}})
    return
  }
  cik := fmt.Sprintf("%010d", best.CIK)
  writeJSON(w, 200, Company{Name:best.Title, CIK:cik, Tickers:[]string{best.Ticker}})
}

/************** latest 10-K / 20-F **************/
type submissions struct {
  Filings struct {
    Recent struct {
      AccessionNumber []string `json:"accessionNumber"`
      Form            []string `json:"form"`
      FilingDate      []string `json:"filingDate"`
      PrimaryDocument []string `json:"primaryDocument"`
    } `json:"recent"`
  } `json:"filings"`
}

type FilingSection struct {
  Title string `json:"title"`
  Text  string `json:"text"`
  URL   string `json:"url"`
}

type Latest10KResponse struct {
  Year      int             `json:"year"`
  FormType  string          `json:"formType"`
  FilingUrl string          `json:"filingUrl"`
  Sections  []FilingSection `json:"sections"`
}

func parseYear(date string) int {
  if len(date) >= 4 {
    y, _ := strconv.Atoi(date[:4])
    return y
  }
  return 0
}

func stripSpaces(s string) string {
  re := regexp.MustCompile(`\s+`)
  return strings.TrimSpace(re.ReplaceAllString(s, " "))
}

// 必杀技1：按 Item 切分（Item 1 / 1A / 7 / 7A / 8）
func splitByItems(all string, baseURL string) []FilingSection {
  text := stripSpaces(all)
  if len(text) < 2000 {
    return []FilingSection{{Title:"全文", Text:text, URL:baseURL}}
  }

  patterns := []struct{
    title string
    re *regexp.Regexp
  }{
    {"Item 1 Business / 业务", regexp.MustCompile(`(?i)\bitem\s+1\.\s`)},
    {"Item 1A Risk Factors / 风险因素", regexp.MustCompile(`(?i)\bitem\s+1a\.\s`)},
    {"Item 7 MD&A / 管理层讨论分析", regexp.MustCompile(`(?i)\bitem\s+7\.\s`)},
    {"Item 7A Quantitative & Qualitative / 市场风险", regexp.MustCompile(`(?i)\bitem\s+7a\.\s`)},
    {"Item 8 Financial Statements / 财务报表", regexp.MustCompile(`(?i)\bitem\s+8\.\s`)},
  }

  type hit struct{ title string; pos int }
  hits := []hit{}
  for _, p := range patterns {
    loc := p.re.FindStringIndex(text)
    if loc != nil {
      hits = append(hits, hit{title:p.title, pos: loc[0]})
    }
  }
  if len(hits) < 2 {
    n := 6
    size := len(text)/n
    out := []FilingSection{}
    for i:=0;i<n;i++{
      a := i*size
      b := a+size
      if i==n-1 { b = len(text) }
      part := strings.TrimSpace(text[a:b])
      if len(part) > 200 { out = append(out, FilingSection{Title:fmt.Sprintf("全文片段-%d", i+1), Text:part, URL:baseURL}) }
    }
    return out
  }

  sort.Slice(hits, func(i,j int) bool { return hits[i].pos < hits[j].pos })

  out := []FilingSection{}
  for i := 0; i < len(hits); i++ {
    start := hits[i].pos
    end := len(text)
    if i < len(hits)-1 {
      end = hits[i+1].pos
    }
    part := strings.TrimSpace(text[start:end])
    if len(part) > 200 {
      out = append(out, FilingSection{Title:hits[i].title, Text:part, URL:baseURL})
    }
  }
  return out
}

func handleLatest10K(w http.ResponseWriter, r *http.Request) {
  cik := strings.TrimSpace(r.URL.Query().Get("cik"))
  ua := r.Header.Get("User-Agent")
  if ua == "" { ua = "FinAssistantChampion/1.0 (email: you@example.com)" }
  if cik == "" { writeJSON(w, 400, map[string]any{"error":"missing cik"}); return }

  subURL := fmt.Sprintf("https://data.sec.gov/submissions/CIK%s.json", cik)
  b, err := httpGET(subURL, ua)
  if err != nil { writeJSON(w, 500, map[string]any{"error":err.Error()}); return }

  var sub submissions
  if err := json.Unmarshal(b, &sub); err != nil { writeJSON(w, 500, map[string]any{"error":err.Error()}); return }

  type cand struct{ idx int; date string }
  cands := []cand{}
  for i := range sub.Filings.Recent.Form {
    f := sub.Filings.Recent.Form[i]
    if f == "10-K" || f == "20-F" {
      cands = append(cands, cand{idx:i, date: sub.Filings.Recent.FilingDate[i]})
    }
  }
  if len(cands) == 0 {
    writeJSON(w, 200, map[string]any{"filingUrl":"", "sections":[]any{}})
    return
  }
  sort.Slice(cands, func(i,j int) bool { return cands[i].date > cands[j].date })
  i := cands[0].idx

  acc := sub.Filings.Recent.AccessionNumber[i]
  form := sub.Filings.Recent.Form[i]
  filed := sub.Filings.Recent.FilingDate[i]
  primary := sub.Filings.Recent.PrimaryDocument[i]
  fy := parseYear(filed)

  accNoDash := strings.ReplaceAll(acc, "-", "")
  filingURL := fmt.Sprintf("https://www.sec.gov/Archives/edgar/data/%s/%s/%s", strings.TrimLeft(cik, "0"), accNoDash, primary)

  htmlBytes, err := httpGET(filingURL, ua)
  if err != nil { writeJSON(w, 500, map[string]any{"error":err.Error()}); return }

  doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(htmlBytes)))
  var allText string
  if err != nil { allText = string(htmlBytes) } else { allText = doc.Text() }

  sections := splitByItems(allText, filingURL)
  writeJSON(w, 200, Latest10KResponse{Year:fy, FormType:form, FilingUrl:filingURL, Sections:sections})
}

/************** companyfacts **************/
type FactPoint struct {
  FY    int     `json:"fy"`
  Value float64 `json:"value"`
  Unit  string  `json:"unit"`
  End   string  `json:"end"`
  Form  string  `json:"form"`
}

type CompanyFactsResponse struct {
  Series    map[string][]FactPoint `json:"series"`
  SourceUrl string                `json:"sourceUrl"`
}

func toFloat(v any) (float64, bool) {
  switch t := v.(type) {
  case float64: return t, true
  case int: return float64(t), true
  case json.Number:
    f, err := t.Float64()
    return f, err == nil
  default: return 0, false
  }
}
func toInt(v any) (int, bool) {
  switch t := v.(type) {
  case float64: return int(t), true
  case int: return t, true
  case json.Number:
    i, err := t.Int64()
    return int(i), err == nil
  default: return 0, false
  }
}

func handleCompanyFacts(w http.ResponseWriter, r *http.Request) {
  cik := strings.TrimSpace(r.URL.Query().Get("cik"))
  ua := r.Header.Get("User-Agent")
  if ua == "" { ua = "FinAssistantChampion/1.0 (email: you@example.com)" }
  if cik == "" { writeJSON(w, 400, map[string]any{"error":"missing cik"}); return }

  metricsStr := strings.TrimSpace(r.URL.Query().Get("metrics"))
  yearsStr := strings.TrimSpace(r.URL.Query().Get("years"))

  metrics := []string{}
  if metricsStr != "" {
    for _, m := range strings.Split(metricsStr, ",") {
      m = strings.TrimSpace(m)
      if m != "" { metrics = append(metrics, m) }
    }
  }
  years := map[int]bool{}
  if yearsStr != "" {
    for _, y := range strings.Split(yearsStr, ",") {
      yy, err := strconv.Atoi(strings.TrimSpace(y))
      if err == nil { years[yy] = true }
    }
  }

  url := fmt.Sprintf("https://data.sec.gov/api/xbrl/companyfacts/CIK%s.json", cik)
  b, err := httpGET(url, ua)
  if err != nil { writeJSON(w, 500, map[string]any{"error":err.Error()}); return }

  var raw map[string]any
  if err := json.Unmarshal(b, &raw); err != nil { writeJSON(w, 500, map[string]any{"error":err.Error()}); return }

  facts, _ := raw["facts"].(map[string]any)
  usgaap, _ := facts["us-gaap"].(map[string]any)

  mapping := map[string]string{
    "Revenue": "Revenues",
    "RAndD": "ResearchAndDevelopmentExpense",
    "GrossProfit":"GrossProfit",
    "OperatingIncome":"OperatingIncomeLoss",
    "NetIncome":"NetIncomeLoss",
  }

  if len(metrics) == 0 {
    metrics = []string{"Revenue","RAndD","GrossProfit","GrossMargin","OperatingIncome","NetIncome"}
  }

  series := map[string][]FactPoint{}

  for _, m := range metrics {
    if m == "GrossMargin" { continue }
    tag := mapping[m]
    if tag == "" { tag = m }
    obj, ok := usgaap[tag].(map[string]any)
    if !ok { continue }
    units, _ := obj["units"].(map[string]any)

    var unit string
    var arr []any
    if v, ok := units["USD"].([]any); ok { unit = "USD"; arr = v } else {
      for k, v := range units {
        if vv, ok := v.([]any); ok && len(vv)>0 { unit=k; arr=vv; break }
      }
    }
    if len(arr) == 0 { continue }

    tmp := []FactPoint{}
    for _, it := range arr {
      row, _ := it.(map[string]any)
      fy, _ := toInt(row["fy"])
      if fy == 0 { continue }
      if len(years) > 0 && !years[fy] { continue }
      form, _ := row["form"].(string)
      if form != "10-K" && form != "20-F" { continue }
      val, ok := toFloat(row["val"])
      if !ok { continue }
      end, _ := row["end"].(string)
      tmp = append(tmp, FactPoint{FY:fy, Value:val, Unit:unit, End:end, Form:form})
    }

    uniq := map[int]FactPoint{}
    for _, p := range tmp {
      if old, ok := uniq[p.FY]; !ok || p.End > old.End { uniq[p.FY] = p }
    }
    out := []FactPoint{}
    for _, p := range uniq { out = append(out, p) }
    sort.Slice(out, func(i,j int) bool { return out[i].FY < out[j].FY })
    series[m] = out
  }

  // GrossMargin = GrossProfit / Revenue
  if gp, ok1 := series["GrossProfit"]; ok1 {
    if rev, ok2 := series["Revenue"]; ok2 {
      rm := map[int]float64{}
      for _, r := range rev { rm[r.FY] = r.Value }
      out := []FactPoint{}
      for _, g := range gp {
        r := rm[g.FY]
        if r == 0 { continue }
        out = append(out, FactPoint{FY:g.FY, Value:g.Value/r, Unit:"ratio", End:g.End, Form:g.Form})
      }
      series["GrossMargin"] = out
    }
  }

  writeJSON(w, 200, CompanyFactsResponse{Series:series, SourceUrl:url})
}
