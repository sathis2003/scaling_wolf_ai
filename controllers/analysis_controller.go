package controllers

import (
    "bytes"
    "context"
    "encoding/csv"
    "encoding/json"
    "crypto/sha256"
    "encoding/hex"
    "io"
    "math"
    "net/http"
    "path/filepath"
    "regexp"
    "strconv"
    "strings"
    "time"

    "github.com/gin-gonic/gin"
    "github.com/google/generative-ai-go/genai"
    "github.com/xuri/excelize/v2"
    "google.golang.org/api/option"

    "scalingwolf-ai/backend/config"
    "scalingwolf-ai/backend/database"
    "scalingwolf-ai/backend/utils"
)

// UploadAnalyze handles CSV/XLSX upload, detects header + columns, cleans rows,
// and returns metrics: total_sales, bill_row_count, unique_bill_count.
func UploadAnalyze(cfg config.Config) gin.HandlerFunc {
    return func(c *gin.Context) {
        // Accept multipart/form-data with field name "file"
        file, header, err := c.Request.FormFile("file")
        if err != nil {
            c.JSON(http.StatusBadRequest, gin.H{"error": "missing file (field 'file')"})
            return
        }
        defer file.Close()

        // Read entire file into memory (simplifies parsing both CSV and XLSX)
        buf, err := io.ReadAll(file)
        if err != nil {
            c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read file"})
            return
        }

        ext := strings.ToLower(filepath.Ext(header.Filename))
        if ext != ".csv" && ext != ".xlsx" && ext != ".xls" {
            c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported file type; use .csv or .xlsx/.xls"})
            return
        }

        // Parse rows from file (no header yet)
        allRows, err := readAllRows(buf, ext)
        if err != nil {
            c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
            return
        }
        // Preview first 5 rows for AI detection
        preview := firstNRows(allRows, 5)

        // Detect header + columns: try cache -> Gemini -> heuristic
        sig := signatureForPreview(preview)
        headerRowIdx, salesHeaderText, billHeaderText, aiUsed, aiMsg := cachedMappingOrDetect(cfg, c.GetInt64("user_id"), sig, preview)
        if headerRowIdx < 0 || salesHeaderText == "" || billHeaderText == "" {
            // Fallback heuristic on the full data if AI failed
            headerRowIdx, salesHeaderText, billHeaderText = heuristicDetect(allRows)
        }
        if headerRowIdx < 0 {
            c.JSON(http.StatusBadRequest, gin.H{"error": "could not detect header row"})
            return
        }

        // Build header from the detected row
        headers := normalizeHeaders(allRows, headerRowIdx)
        if len(headers) == 0 {
            c.JSON(http.StatusBadRequest, gin.H{"error": "empty header row"})
            return
        }

        // Map detected names to actual header names (case-insensitive + substring)
        salesCol := findColumn(headers, salesHeaderText)
        billCol := findColumn(headers, billHeaderText)
        if salesCol == "" || billCol == "" {
            c.JSON(http.StatusBadRequest, gin.H{
                "error":            "could not match detected columns",
                "headers":          headers,
                "sales_detected":   salesHeaderText,
                "bill_detected":    billHeaderText,
            })
            return
        }

        // Build records (rows after header)
        records := buildRecords(allRows, headerRowIdx, headers)

        // Cleaning pipeline
        preRows := len(records)
        records = dropBlankRows(records)
        droppedBlank := preRows - len(records)

        records, removedSecond := dropIfSecondColumnTotalish(records, headers)
        droppedSecond := len(removedSecond)

        records, removedSummary := filterSummaryRows(records, billCol, salesCol)
        droppedSummary := len(removedSummary)

        // Enforce rule: use exactly rows with a bill present
        used := make([]map[string]string, 0, len(records))
        for _, r := range records {
            if !isEffectivelyEmptyBill(r[billCol]) {
                used = append(used, r)
            }
        }

        // Sales sum strictly from the same rows
        var totalSales float64
        for _, r := range used {
            v := toNumeric(r[salesCol])
            if !math.IsNaN(v) {
                totalSales += v
            }
        }

        billRowsCount := len(used)
        uniqueBill := uniqueCount(used, billCol)

        // Optional short summary via Gemini
        summary := ""
        if cfg.GeminiAPIKey != "" {
            summary = geminiSummary(cfg, totalSales, billRowsCount, uniqueBill)
        }
        if summary == "" {
            summary = simpleSummary(totalSales, billRowsCount, uniqueBill)
        }

        // Persist to DB sales_metrics and index a small RAG doc
        {
            payload := map[string]any{
                "file_name": header.Filename,
                "headers": headers,
            }
            pb, _ := json.Marshal(payload)
            ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
            defer cancel()
            _, _ = database.Pool.Exec(ctx, `INSERT INTO sales_metrics(user_id, source_type, payload, total_sales, bill_row_count, unique_bill_count) VALUES($1,'file',$2::jsonb,$3,$4,$5)`, c.GetInt64("user_id"), string(pb), round2(totalSales), billRowsCount, uniqueBill)
            // Upsert column mapping cache
            _, _ = database.Pool.Exec(ctx, `INSERT INTO column_mappings(user_id, signature, header_row, sales_column, bill_column) VALUES($1,$2,$3,$4,$5)
                ON CONFLICT (user_id, signature) DO UPDATE SET header_row=EXCLUDED.header_row, sales_column=EXCLUDED.sales_column, bill_column=EXCLUDED.bill_column`,
                c.GetInt64("user_id"), sig, headerRowIdx, salesCol, billCol,
            )
            // Optional RAG index if Gemini key is configured
            if cfg.GeminiAPIKey != "" {
                doc := "Sales metrics summary: Total sales = " + strconv.FormatFloat(round2(totalSales), 'f', 2, 64) + ", bill rows = " + strconv.Itoa(billRowsCount) + ", unique bill IDs = " + strconv.Itoa(uniqueBill)
                aiClient, err := utils.NewAIClient(ctx, utils.AIConfig{APIKey: cfg.GeminiAPIKey, GenModel: cfg.GeminiModel, EmbedModel: cfg.GeminiEmbeddingModel})
                if err == nil {
                    emb, err := utils.EmbedText(ctx, aiClient, cfg.GeminiEmbeddingModel, doc)
                    aiClient.Close()
                    if err == nil {
                        vec := utils.VectorLiteral(emb)
                        _, _ = database.Pool.Exec(ctx, `INSERT INTO rag_documents(user_id, content, metadata, embedding) VALUES($1,$2,$3::jsonb, $4::vector)`, c.GetInt64("user_id"), doc, `{"source":"sales_metrics"}`, vec)
                    }
                }
            }
        }

        // Build response JSON similar to Python
        resp := gin.H{
            "summary": summary,
            "metrics": gin.H{
                "total_sales":      round2(totalSales),
                "bill_row_count":   billRowsCount,
                "unique_bill_count": uniqueBill,
            },
            "meta": gin.H{
                "file_name":    header.Filename,
                "header_row":   headerRowIdx,
                "sales_column": salesCol,
                "bill_column":  billCol,
                "ai_used":      aiUsed,
                "ai_message":   aiMsg,
            },
            "cleaning": gin.H{
                "dropped_blank_rows":          droppedBlank,
                "dropped_totalish_second_col": droppedSecond,
                "dropped_summary_rows":        droppedSummary,
                "final_rows_used":             billRowsCount,
            },
        }

        c.JSON(http.StatusOK, resp)
    }
}

// -------------------- File reading helpers --------------------

func readAllRows(content []byte, ext string) ([][]string, error) {
    switch ext {
    case ".csv":
        r := csv.NewReader(bytes.NewReader(content))
        r.FieldsPerRecord = -1 // allow variable columns
        rows, err := r.ReadAll()
        if err != nil {
            return nil, err
        }
        return rows, nil
    case ".xlsx", ".xls":
        f, err := excelize.OpenReader(bytes.NewReader(content))
        if err != nil {
            return nil, err
        }
        sheets := f.GetSheetList()
        if len(sheets) == 0 {
            return [][]string{}, nil
        }
        sheet := sheets[0]
        rows := [][]string{}
        rs, err := f.Rows(sheet)
        if err != nil {
            return nil, err
        }
        for rs.Next() {
            r, err := rs.Columns()
            if err != nil {
                return nil, err
            }
            rows = append(rows, r)
        }
        return rows, nil
    default:
        return nil, io.ErrUnexpectedEOF
    }
}

func firstNRows(rows [][]string, n int) [][]string {
    if len(rows) <= n {
        return rows
    }
    cp := make([][]string, n)
    copy(cp, rows[:n])
    return cp
}

func signatureForPreview(preview [][]string) string {
    // hash JSON of preview rows for a quick signature
    b, _ := json.Marshal(preview)
    sum := sha256.Sum256(b)
    return hex.EncodeToString(sum[:])
}

// -------------------- Header detection --------------------

func detectHeaderAndColumns(cfg config.Config, preview [][]string) (int, string, string, bool, string) {
    if cfg.GeminiAPIKey == "" {
        return -1, "", "", false, "Gemini API key not configured"
    }

    // Prepare a Pandas-like orient='split' JSON for the first 5 rows
    maxCols := 0
    for _, r := range preview {
        if len(r) > maxCols {
            maxCols = len(r)
        }
    }
    cols := make([]string, maxCols)
    for i := 0; i < maxCols; i++ {
        cols[i] = "col" + strconv.Itoa(i)
    }
    split := map[string]any{
        "columns": cols,
        "index":   make([]int, len(preview)),
        "data":    preview,
    }
    splitJSON, _ := json.Marshal(split)

    prompt := "" +
        "You are a data understanding AI.\n\n" +
        "Given the first 5 rows of a tabular file, identify:\n" +
        "1) Which row (0-based index) is most likely the header (column names).\n" +
        "2) The exact column name that represents \"Sales\" or \"Amount\".\n" +
        "3) The exact column name that represents \"Bill\" or \"Invoice\".\n\n" +
        "Important:\n- Return STRICT JSON only, no commentary, no markdown fences.\n- Use keys exactly: header_row_index, sales_column, bill_column.\n\n" +
        "Example format:\n{" +
        "\"header_row_index\": 0, \"sales_column\": \"Item Net Amt\", \"bill_column\": \"Bill No\"}\n\n" +
        "Here are the first 5 rows (Pandas JSON with orient='split'):\n" + string(splitJSON)

    ctx := context.Background()
    client, err := genai.NewClient(ctx, option.WithAPIKey(cfg.GeminiAPIKey))
    if err != nil {
        return -1, "", "", false, "Gemini client error"
    }
    defer client.Close()

    model := client.GenerativeModel(cfg.GeminiModel)
    resp, err := model.GenerateContent(ctx, genai.Text(prompt))
    if err != nil || resp == nil || len(resp.Candidates) == 0 {
        return -1, "", "", false, "Gemini generate error"
    }
    text := extractText(resp)
    if text == "" {
        return -1, "", "", true, "Gemini returned empty"
    }
    cleaned := stripFences(text)
    var out struct{
        HeaderRowIndex int    `json:"header_row_index"`
        SalesColumn    string `json:"sales_column"`
        BillColumn     string `json:"bill_column"`
    }
    if err := json.Unmarshal([]byte(cleaned), &out); err != nil {
        return -1, "", "", true, "Gemini JSON parse error"
    }
    return out.HeaderRowIndex, strings.TrimSpace(out.SalesColumn), strings.TrimSpace(out.BillColumn), true, "ok"
}

func cachedMappingOrDetect(cfg config.Config, userID int64, signature string, preview [][]string) (int, string, string, bool, string) {
    // check cache first
    if signature != "" {
        ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
        defer cancel()
        var headerRow int
        var salesCol, billCol string
        err := database.Pool.QueryRow(ctx, `SELECT header_row, sales_column, bill_column FROM column_mappings WHERE user_id=$1 AND signature=$2`, userID, signature).Scan(&headerRow, &salesCol, &billCol)
        if err == nil {
            return headerRow, salesCol, billCol, false, "cache"
        }
    }
    // fallback to AI
    hr, sc, bc, used, msg := detectHeaderAndColumns(cfg, preview)
    return hr, sc, bc, used, msg
}

func stripFences(s string) string {
    t := strings.TrimSpace(s)
    t = strings.TrimPrefix(t, "```json")
    t = strings.TrimPrefix(t, "```")
    t = strings.TrimSuffix(t, "```")
    return strings.TrimSpace(t)
}

func extractText(resp *genai.GenerateContentResponse) string {
    if resp == nil || len(resp.Candidates) == 0 {
        return ""
    }
    var b strings.Builder
    for _, c := range resp.Candidates {
        if c == nil || c.Content == nil {
            continue
        }
        for _, p := range c.Content.Parts {
            if s, ok := p.(genai.Text); ok {
                b.WriteString(string(s))
            }
        }
    }
    return b.String()
}

func heuristicDetect(rows [][]string) (int, string, string) {
    headerIdx := -1
    bestScore := -1.0
    for i, r := range rows {
        nonEmpty := 0
        alpha := 0
        for _, v := range r {
            t := strings.TrimSpace(v)
            if t == "" {
                continue
            }
            nonEmpty++
            if hasLetter(t) {
                alpha++
            }
        }
        if nonEmpty == 0 {
            continue
        }
        score := float64(alpha) / float64(nonEmpty)
        if score >= 0.5 && score > bestScore {
            bestScore = score
            headerIdx = i
        }
        if i >= 4 { // look at first ~5 rows
            break
        }
    }
    if headerIdx == -1 {
        headerIdx = 0
    }
    headers := normalizeHeaders(rows, headerIdx)
    sales := pickColumn(headers, []string{"sales", "amount", "amt", "net amt", "net amount", "total", "grand total", "invoice amount", "subtotal", "item net amt"})
    bill := pickColumn(headers, []string{"bill", "bill no", "bill number", "invoice", "invoice no", "invoice number", "inv", "ref no", "reference", "voucher", "receipt"})
    return headerIdx, sales, bill
}

func hasLetter(s string) bool {
    for _, ch := range s {
        if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') {
            return true
        }
    }
    return false
}

func normalizeHeaders(rows [][]string, headerIdx int) []string {
    if headerIdx < 0 || headerIdx >= len(rows) {
        return nil
    }
    raw := rows[headerIdx]
    headers := make([]string, len(raw))
    for i, v := range raw {
        t := strings.TrimSpace(v)
        if t == "" {
            t = "Col" + strconv.Itoa(i)
        }
        headers[i] = t
    }
    return headers
}

func pickColumn(headers []string, keywords []string) string {
    // exact match preferred
    for _, k := range keywords {
        for _, h := range headers {
            if strings.EqualFold(h, k) {
                return h
            }
        }
    }
    // substring fallback
    for _, k := range keywords {
        lk := strings.ToLower(k)
        for _, h := range headers {
            if strings.Contains(strings.ToLower(h), lk) {
                return h
            }
        }
    }
    return ""
}

func findColumn(headers []string, target string) string {
    t := strings.TrimSpace(strings.ToLower(target))
    for _, h := range headers {
        if strings.ToLower(strings.TrimSpace(h)) == t {
            return h
        }
    }
    for _, h := range headers {
        if t != "" && strings.Contains(strings.ToLower(h), t) {
            return h
        }
    }
    return ""
}

func buildRecords(rows [][]string, headerIdx int, headers []string) []map[string]string {
    out := make([]map[string]string, 0, len(rows)-headerIdx-1)
    for i := headerIdx + 1; i < len(rows); i++ {
        r := rows[i]
        m := make(map[string]string, len(headers))
        for j := 0; j < len(headers); j++ {
            var v string
            if j < len(r) {
                v = strings.TrimSpace(r[j])
            }
            m[headers[j]] = v
        }
        out = append(out, m)
    }
    return out
}

// -------------------- Cleaning helpers --------------------

var emptyBill = map[string]struct{}{
    "": {}, "-": {}, "na": {}, "n/a": {}, "none": {}, "null": {}, "nil": {}, "nan": {}, "0": {},
}

func isEffectivelyEmptyBill(x string) bool {
    t := strings.TrimSpace(strings.ToLower(x))
    _, ok := emptyBill[t]
    return ok
}

func dropBlankRows(rows []map[string]string) []map[string]string {
    out := make([]map[string]string, 0, len(rows))
    for _, r := range rows {
        nonempty := false
        for _, v := range r {
            if strings.TrimSpace(v) != "" {
                nonempty = true
                break
            }
        }
        if nonempty {
            out = append(out, r)
        }
    }
    return out
}

var totalRegex = regexp.MustCompile(`(?i)\b(grand\s*)?sub\s*total\b|\bgrand\s*total\b|\btotal\b`)

func looksLikeTotalWord(s string) bool {
    t := strings.TrimSpace(strings.ToLower(s))
    if t == "" {
        return false
    }
    if totalRegex.MatchString(t) {
        return true
    }
    // very small fuzzy: allow one edit away from "total"
    return editDistance(t, "total") <= 1
}

func rowIsTotalish(r map[string]string) bool {
    for _, v := range r {
        if looksLikeTotalWord(v) {
            return true
        }
    }
    return false
}

func dropIfSecondColumnTotalish(rows []map[string]string, headers []string) ([]map[string]string, map[int]struct{}) {
    removed := map[int]struct{}{}
    if len(rows) == 0 {
        return rows, removed
    }
    // Identify the second column by original header order
    if len(headers) < 2 {
        return rows, removed
    }
    second := headers[1]

    kept := make([]map[string]string, 0, len(rows))
    for i, r := range rows {
        if looksLikeTotalWord(r[second]) {
            removed[i] = struct{}{}
            continue
        }
        kept = append(kept, r)
    }
    return kept, removed
}

func filterSummaryRows(rows []map[string]string, billCol, salesCol string) ([]map[string]string, map[int]struct{}) {
    removed := map[int]struct{}{}
    kept := make([]map[string]string, 0, len(rows))
    for i, r := range rows {
        // rule 1: any 'total-ish' word anywhere
        if rowIsTotalish(r) {
            removed[i] = struct{}{}
            continue
        }
        // rule 2: bill missing, sales numeric present, almost nothing else
        billMissing := isEffectivelyEmptyBill(r[billCol])
        salesVal := toNumeric(r[salesCol])
        if billMissing && !math.IsNaN(salesVal) {
            nonSalesNonEmpty := 0
            for k, v := range r {
                if k == salesCol {
                    continue
                }
                if strings.TrimSpace(v) != "" && strings.ToLower(strings.TrimSpace(v)) != "nan" {
                    nonSalesNonEmpty++
                }
            }
            if nonSalesNonEmpty <= 1 {
                removed[i] = struct{}{}
                continue
            }
        }
        kept = append(kept, r)
    }
    return kept, removed
}

func toNumeric(s string) float64 {
    t := strings.TrimSpace(s)
    if t == "" {
        return math.NaN()
    }
    // remove non digits except dot and minus
    cleaned := make([]rune, 0, len(t))
    for _, ch := range t {
        if (ch >= '0' && ch <= '9') || ch == '.' || ch == '-' {
            cleaned = append(cleaned, ch)
        }
    }
    if len(cleaned) == 0 {
        return math.NaN()
    }
    f, err := strconv.ParseFloat(string(cleaned), 64)
    if err != nil {
        return math.NaN()
    }
    return f
}

func uniqueCount(rows []map[string]string, col string) int {
    seen := map[string]struct{}{}
    for _, r := range rows {
        v := strings.TrimSpace(r[col])
        if v == "" {
            continue
        }
        seen[v] = struct{}{}
    }
    return len(seen)
}

func editDistance(a, b string) int {
    // simple Levenshtein distance
    la, lb := len(a), len(b)
    if la == 0 {
        return lb
    }
    if lb == 0 {
        return la
    }
    dp := make([][]int, la+1)
    for i := range dp {
        dp[i] = make([]int, lb+1)
    }
    for i := 0; i <= la; i++ {
        dp[i][0] = i
    }
    for j := 0; j <= lb; j++ {
        dp[0][j] = j
    }
    for i := 1; i <= la; i++ {
        for j := 1; j <= lb; j++ {
            cost := 0
            if a[i-1] != b[j-1] {
                cost = 1
            }
            dp[i][j] = min(
                dp[i-1][j]+1,
                dp[i][j-1]+1,
                dp[i-1][j-1]+cost,
            )
        }
    }
    return dp[la][lb]
}

func min(a, b, c int) int {
    m := a
    if b < m { m = b }
    if c < m { m = c }
    return m
}

func round2(f float64) float64 {
    return math.Round(f*100) / 100
}

// -------------------- AI summary --------------------

func geminiSummary(cfg config.Config, total float64, rows, uniq int) string {
    if cfg.GeminiAPIKey == "" { return "" }
    ctx := context.Background()
    client, err := genai.NewClient(ctx, option.WithAPIKey(cfg.GeminiAPIKey))
    if err != nil { return "" }
    defer client.Close()
    model := client.GenerativeModel(cfg.GeminiModel)
    prompt := "Create a short, friendly one-sentence summary for a user.\n" +
        "Facts:\n" +
        "- Total sales = " + strconv.FormatFloat(total, 'f', 2, 64) + "\n" +
        "- Bill row count = " + strconv.Itoa(rows) + "\n" +
        "- Unique bill IDs = " + strconv.Itoa(uniq) + "\n" +
        "Keep it concise and neutral (no emojis)."
    resp, err := model.GenerateContent(ctx, genai.Text(prompt))
    if err != nil || resp == nil { return "" }
    return strings.TrimSpace(extractText(resp))
}

func simpleSummary(total float64, rows, uniq int) string {
    return "Total sales = " + strconv.FormatFloat(round2(total), 'f', 2, 64) + ", bill rows = " + strconv.Itoa(rows) + ", unique bill IDs = " + strconv.Itoa(uniq) + "."
}
