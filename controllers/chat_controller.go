package controllers

import (
    "context"
    "database/sql"
    "fmt"
    "net/http"
    "strconv"
    "strings"
    "time"
    "log"
    "io"
    "path/filepath"
    "mime/multipart"
    "encoding/json"
    "math"
    "regexp"

    "github.com/gin-gonic/gin"
    "github.com/google/generative-ai-go/genai"
    "scalingwolf-ai/backend/config"
    "scalingwolf-ai/backend/database"
    "scalingwolf-ai/backend/utils"
)

type ChatSendRequest struct {
    ChatID  *int64  `json:"chat_id"`
    Message string  `json:"message"`
}

type ChatSendResponse struct {
    ChatID         int64       `json:"chat_id"`
    Reply          string      `json:"reply"`
    RetrievedDocs  []string    `json:"retrieved_docs"`
    Ingestions     []IngestionResult `json:"ingestions,omitempty"`
    Tokens         *struct{
        Input  int64 `json:"input"`
        Output int64 `json:"output"`
        Total  int64 `json:"total"`
    } `json:"tokens,omitempty"`
}

type IngestionResult struct {
    Type           string   `json:"type"` // sales_metrics | knowledge | ambiguous | error
    FileName       string   `json:"file_name,omitempty"`
    Status         string   `json:"status"` // ok | ambiguous | error
    Notes          string   `json:"notes,omitempty"`
    ChunksIndexed  *int     `json:"chunks_indexed,omitempty"`
    Metrics        *struct {
        TotalSales      float64 `json:"total_sales"`
        BillRowCount    int     `json:"bill_row_count"`
        UniqueBillCount int     `json:"unique_bill_count"`
    } `json:"metrics,omitempty"`
}

type ChatRow struct {
    ID        int64     `json:"id"`
    Title     string    `json:"title"`
    CreatedAt time.Time `json:"created_at"`
    LastMsgAt time.Time `json:"last_message_at"`
}

type ChatTitleRequest struct { Title string `json:"title"` }

func ChatCreate() gin.HandlerFunc {
    return func(c *gin.Context) {
        uid := c.GetInt64("user_id")
        var req ChatTitleRequest
        _ = c.ShouldBindJSON(&req)
        if strings.TrimSpace(req.Title) == "" { req.Title = "New Chat" }
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        var id int64
        if err := database.Pool.QueryRow(ctx, `INSERT INTO chats(user_id,title) VALUES($1,$2) RETURNING id`, uid, req.Title).Scan(&id); err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error":"db error"}); return
        }
        c.JSON(http.StatusOK, gin.H{"chat_id": id})
    }
}

func ChatList() gin.HandlerFunc {
    return func(c *gin.Context) {
        uid := c.GetInt64("user_id")
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        rows, err := database.Pool.Query(ctx, `
            SELECT c.id, c.title, c.created_at, COALESCE(MAX(m.created_at), c.created_at) AS last_msg
            FROM chats c
            LEFT JOIN chat_messages m ON m.chat_id = c.id
            WHERE c.user_id=$1
            GROUP BY c.id, c.title, c.created_at
            ORDER BY last_msg DESC` , uid)
        if err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error":"db error"}); return }
        defer rows.Close()
        list := []ChatRow{}
        for rows.Next() {
            var r ChatRow
            if err := rows.Scan(&r.ID, &r.Title, &r.CreatedAt, &r.LastMsgAt); err == nil { list = append(list, r) }
        }
        c.JSON(http.StatusOK, list)
    }
}

func ChatGetMessages() gin.HandlerFunc {
    return func(c *gin.Context) {
        uid := c.GetInt64("user_id")
        chatIDStr := c.Param("id")
        chatID, _ := strconv.ParseInt(chatIDStr, 10, 64)
        ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
        defer cancel()
        // ownership check
        var exists bool
        _ = database.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM chats WHERE id=$1 AND user_id=$2)`, chatID, uid).Scan(&exists)
        if !exists { c.JSON(http.StatusNotFound, gin.H{"error":"chat not found"}); return }
        rows, err := database.Pool.Query(ctx, `SELECT id, role, content, created_at FROM chat_messages WHERE chat_id=$1 ORDER BY id ASC`, chatID)
        if err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error":"db error"}); return }
        defer rows.Close()
        type Msg struct { ID int64 `json:"id"`; Role string `json:"role"`; Content string `json:"content"`; CreatedAt time.Time `json:"created_at"` }
        msgs := []Msg{}
        for rows.Next() {
            var m Msg
            rows.Scan(&m.ID, &m.Role, &m.Content, &m.CreatedAt)
            msgs = append(msgs, m)
        }
        c.JSON(http.StatusOK, msgs)
    }
}

func ChatRename() gin.HandlerFunc {
    return func(c *gin.Context) {
        uid := c.GetInt64("user_id")
        chatIDStr := c.Param("id")
        chatID, _ := strconv.ParseInt(chatIDStr, 10, 64)
        var req ChatTitleRequest
        if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Title)=="" { c.JSON(http.StatusBadRequest, gin.H{"error":"invalid title"}); return }
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        res, err := database.Pool.Exec(ctx, `UPDATE chats SET title=$1 WHERE id=$2 AND user_id=$3`, req.Title, chatID, uid)
        if err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error":"db error"}); return }
        if res.RowsAffected() == 0 { c.JSON(http.StatusNotFound, gin.H{"error":"chat not found"}); return }
        c.JSON(http.StatusOK, gin.H{"status":"ok"})
    }
}

func ChatDelete() gin.HandlerFunc {
    return func(c *gin.Context) {
        uid := c.GetInt64("user_id")
        chatIDStr := c.Param("id")
        chatID, _ := strconv.ParseInt(chatIDStr, 10, 64)
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        res, err := database.Pool.Exec(ctx, `DELETE FROM chats WHERE id=$1 AND user_id=$2`, chatID, uid)
        if err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error":"db error"}); return }
        if res.RowsAffected() == 0 { c.JSON(http.StatusNotFound, gin.H{"error":"chat not found"}); return }
        c.JSON(http.StatusOK, gin.H{"status":"deleted"})
    }
}

func ChatSend(cfg config.Config) gin.HandlerFunc {
    return func(c *gin.Context) {
        var req ChatSendRequest
        contentType := c.GetHeader("Content-Type")
        isMultipart := strings.HasPrefix(strings.ToLower(contentType), "multipart/form-data")
        var uploadFile io.ReadCloser
        var uploadHeader *multipart.FileHeader
        var haveFile bool
        var formMessage string
        var formChatID *int64
        if isMultipart {
            // Multipart: accept optional file + message + chat_id
            file, hdr, err := c.Request.FormFile("file")
            if err == nil && file != nil {
                uploadFile = file
                uploadHeader = hdr
                haveFile = true
            }
            msg := c.PostForm("message")
            if strings.TrimSpace(msg) != "" {
                formMessage = msg
            }
            if cidStr := c.PostForm("chat_id"); cidStr != "" {
                if v, err := strconv.ParseInt(cidStr, 10, 64); err == nil { formChatID = &v }
            }
        } else {
            if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Message) == "" {
                c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body or missing message"})
                return
            }
        }
        uid := c.GetInt64("user_id")
        ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
        defer cancel()

        // Ensure chat row
        chatID := int64(0)
        if (isMultipart && formChatID == nil) || (!isMultipart && req.ChatID == nil) {
            title := req.Message
            if isMultipart {
                title = formMessage
            }
            if len(strings.TrimSpace(title)) == 0 { title = "New Chat" }
            if len(title) > 80 { title = title[:80] }
            if err := database.Pool.QueryRow(ctx, `INSERT INTO chats(user_id,title) VALUES($1,$2) RETURNING id`, uid, title).Scan(&chatID); err != nil {
                c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"}); return
            }
        } else {
            if isMultipart { chatID = *formChatID } else { chatID = *req.ChatID }
        }

        // Determine the message text
        var userMsg string
        if isMultipart { userMsg = formMessage } else { userMsg = req.Message }
        if strings.TrimSpace(userMsg) == "" { userMsg = "" }

        // Save user message (even if empty, to preserve timeline when file-only)
        if _, err := database.Pool.Exec(ctx, `INSERT INTO chat_messages(chat_id,role,content) VALUES($1,'user',$2)`, chatID, userMsg); err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"}); return
        }

        // Build AI client
        aiClient, err := utils.NewAIClient(ctx, utils.AIConfig{APIKey: cfg.GeminiAPIKey, GenModel: cfg.GeminiModel, EmbedModel: cfg.GeminiEmbeddingModel})
        if err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error": "ai client error"}); return }
        defer aiClient.Close()

        // Quota check (block AI if exhausted)
        var quota, used int64
        _ = database.Pool.QueryRow(ctx, `SELECT token_quota::bigint, token_used::bigint FROM token_quotas WHERE user_id=$1`, uid).Scan(&quota, &used)
        if quota == 0 { quota = 50000 }
        if used >= quota {
            // Save assistant message with quota notice and skip AI
            reply := "Token limit reached (plan: " + strconv.FormatInt(quota/10000,10) + " points). Please upgrade or wait for reset."
            if _, err := database.Pool.Exec(ctx, `INSERT INTO chat_messages(chat_id,role,content) VALUES($1,'assistant',$2)`, chatID, reply); err != nil {
                c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"}); return
            }
            c.JSON(http.StatusOK, ChatSendResponse{ChatID: chatID, Reply: reply, RetrievedDocs: nil, Ingestions: nil})
            return
        }
        // Optional file ingestion (Phase 1): heuristics only
        ingestions := []IngestionResult{}
        if haveFile && uploadFile != nil && uploadHeader != nil {
            defer uploadFile.Close()
            buf, err := io.ReadAll(uploadFile)
            if err != nil {
                ingestions = append(ingestions, IngestionResult{Type:"error", FileName: uploadHeader.Filename, Status:"error", Notes:"failed to read file"})
            } else {
                ext := strings.ToLower(filepath.Ext(uploadHeader.Filename))
                if ext == ".csv" || ext == ".xlsx" || ext == ".xls" {
                    // Try sales pipeline via heuristics (no AI classification yet)
                    if res := processSalesHeuristic(ctx, cfg, uid, uploadHeader.Filename, buf, ext); res != nil {
                        ingestions = append(ingestions, *res)
                    } else {
                        // Phase 2: AI classification on 5-row preview to decide if sales
                        rows, _ := readAllRows(buf, ext)
                        preview := firstNRows(rows, 5)
                        isSales, _, cerr := aiClassifyIsSales(ctx, cfg, preview)
                        if cerr == nil && isSales {
                            if res2 := processSalesWithAI(ctx, cfg, uid, uploadHeader.Filename, buf, ext); res2 != nil {
                                ingestions = append(ingestions, *res2)
                            } else {
                                // fallback to knowledge if AI said sales but cannot process
                                res := upsertKnowledgeChunks(ctx, cfg, uid, uploadHeader.Filename, tableToText(rows, 200))
                                ingestions = append(ingestions, res)
                            }
                        } else {
                            // treat as knowledge: stringify limited table to text
                            res := upsertKnowledgeChunks(ctx, cfg, uid, uploadHeader.Filename, tableToText(rows, 200))
                            ingestions = append(ingestions, res)
                        }
                    }
                } else {
                    // Non-tabular: knowledge
                    res := upsertKnowledgeChunks(ctx, cfg, uid, uploadHeader.Filename, string(buf))
                    ingestions = append(ingestions, res)
                }
            }
        }

        // RAG retrieve (general business knowledge)
        // Use user's current message (from JSON or multipart)
        retrieved, err := retrieveRAG(ctx, aiClient, cfg, uid, userMsg)
        if err != nil { log.Printf("chat rag retrieve error: %v", err) }

        // Also fetch latest chat summary document (token-thrifty memory)
        if sum := latestChatSummary(ctx, uid, chatID); sum != "" {
            retrieved = append([]string{"Chat summary: " + sum}, retrieved...)
        }

        // Conversation history (basic, last 10 turns)
        rows, err := database.Pool.Query(ctx, `SELECT role, content FROM chat_messages WHERE chat_id=$1 ORDER BY id DESC LIMIT 10`, chatID)
        if err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"}); return }
        defer rows.Close()
        history := make([][2]string, 0, 10)
        for rows.Next() {
            var role, content string
            rows.Scan(&role, &content)
            history = append(history, [2]string{role, content})
        }

        // Fetch latest sales metrics presence for guardrail and guidance
        var haveMetrics bool
        var ts *float64
        var br *int
        var ub *int
        if row := database.Pool.QueryRow(ctx, `SELECT total_sales::float8, bill_row_count::int, unique_bill_count::int FROM sales_metrics WHERE user_id=$1 ORDER BY created_at DESC LIMIT 1`, uid); row != nil {
            if err := row.Scan(&ts, &br, &ub); err == nil && ts != nil && br != nil {
                haveMetrics = true
            }
        }

        // Build prompt with strict domain + invalid question handling
        dataMode := isUserDataQuery(userMsg)
        var parts []genai.Part
        if dataMode {
            sys := strings.Join([]string{
                "You are a business consultant and data steward.",
                "The user is asking about their stored data/profile.",
                "Only summarize what is explicitly provided in UserProfileJSON, SalesMetrics, and RAGDocsBreakdown.",
                "Do not speculate or invent fields. If a field is missing, say it is not available.",
                "Be concise (<= 120 words).",
            }, " ")
            parts = append(parts, genai.Text(sys))
            if pj := buildProfileJSON(ctx, uid); strings.TrimSpace(pj) != "" {
                parts = append(parts, genai.Text("UserProfileJSON: "+pj))
            }
            if ss := latestSalesSnapshot(ctx, uid); strings.TrimSpace(ss) != "" {
                parts = append(parts, genai.Text("SalesMetrics: "+ss))
            }
            if db := ragDocsBreakdown(ctx, uid); strings.TrimSpace(db) != "" {
                parts = append(parts, genai.Text("RAGDocsBreakdown: "+db))
            }
        } else {
            sys := strings.Join([]string{
                "You are a business consultant for the user's company.",
                "Only answer business-related topics (sales, marketing, operations, finance, BEP, metrics, pricing, funnels).",
                "If the user's question is unrelated to business, reply briefly: 'I focus on business topics. Please ask a business question.'",
                "If business data seems required but missing, first ask for total sales and bill counts or to upload a CSV/XLSX via the app.",
                "Personalize using the provided UserProfileJSON and SalesMetrics when available.",
                "If the user asks about their stored data or profile, summarize only what is present in UserProfileJSON, SalesMetrics, and document counts.",
                "Be concise (<= 120 words) and actionable.",
            }, " ")
            parts = append(parts, genai.Text(sys))
            // Inject structured user data for consistent personalization
            if pj := buildProfileJSON(ctx, uid); strings.TrimSpace(pj) != "" {
                parts = append(parts, genai.Text("UserProfileJSON: "+pj))
            }
            if ss := latestSalesSnapshot(ctx, uid); strings.TrimSpace(ss) != "" {
                parts = append(parts, genai.Text("SalesMetrics: "+ss))
            }
            // Inject compact one-line profile summary for personalization (low tokens)
            if p := buildProfileSummary(ctx, uid); strings.TrimSpace(p) != "" {
                parts = append(parts, genai.Text("Profile: "+p))
            }
            if len(retrieved) > 0 {
                ctxBlock := "Context documents:\n" + strings.Join(retrieved, "\n---\n")
                parts = append(parts, genai.Text(ctxBlock))
            }
            if ds := ragDocsSummary(ctx, uid); strings.TrimSpace(ds) != "" {
                parts = append(parts, genai.Text(ds))
            }
            // Include ingestion summaries if any (kept short)
            if len(ingestions) > 0 {
                var b strings.Builder
                b.WriteString("New upload processed: ")
                for i, ing := range ingestions {
                    if i > 0 { b.WriteString("; ") }
                    if ing.Type == "sales_metrics" && ing.Metrics != nil {
                        b.WriteString("sales total=")
                        b.WriteString(strconv.FormatFloat(ing.Metrics.TotalSales,'f',2,64))
                        b.WriteString(", bills=")
                        b.WriteString(strconv.Itoa(ing.Metrics.BillRowCount))
                    } else if ing.Type == "knowledge" {
                        b.WriteString("knowledge added")
                    } else {
                        b.WriteString(ing.Status)
                    }
                }
                parts = append(parts, genai.Text(b.String()))
            }
        }
        // Include whether metrics exist to nudge initial guidance
        if haveMetrics {
            parts = append(parts, genai.Text("Known sales metrics present: yes. Latest totals: total_sales="+strconv.FormatFloat(*ts,'f',2,64)+", bill_row_count="+strconv.Itoa(*br)))
        } else {
            parts = append(parts, genai.Text("Known sales metrics present: no. Ask the user for total sales and bill counts or to upload a CSV/XLSX."))
        }
        // simple linearized history from oldest to newest
        for i := len(history)-1; i >= 0; i-- {
            h := history[i]
            prefix := "User:"
            if h[0] == "assistant" { prefix = "Assistant:" }
            parts = append(parts, genai.Text(prefix+" "+h[1]))
        }
        parts = append(parts, genai.Text("User: "+userMsg))
        parts = append(parts, genai.Text("Assistant:"))

        // Call Gemini directly to capture usage tokens
        var reply string
        var tokIn, tokOut, tokTotal int64
        if aiClient != nil {
            model := aiClient.GenerativeModel(cfg.GeminiModel)
            gresp, gerr := model.GenerateContent(ctx, parts...)
            if gerr == nil && gresp != nil {
                // extract text
                var b strings.Builder
                for _, c := range gresp.Candidates {
                    if c == nil || c.Content == nil { continue }
                    for _, p := range c.Content.Parts {
                        if t, ok := p.(genai.Text); ok { b.WriteString(string(t)) }
                    }
                }
                reply = strings.TrimSpace(b.String())
                if gresp.UsageMetadata != nil {
                    tokIn = int64(gresp.UsageMetadata.PromptTokenCount)
                    tokOut = int64(gresp.UsageMetadata.CandidatesTokenCount)
                    tokTotal = int64(gresp.UsageMetadata.TotalTokenCount)
                }
            } else {
                log.Printf("chat ai generate error: %v", gerr)
            }
        }
        if strings.TrimSpace(reply) == "" {
            reply = "I’m temporarily unable to access the AI service. As a quick next step, review your top lead source, tighten offer messaging, and run one A/B test on the checkout or pricing this week."
        }
        reply = strings.TrimSpace(reply)
        if reply == "" { reply = "(no response)" }

        // Save assistant message
        if _, err := database.Pool.Exec(ctx, `INSERT INTO chat_messages(chat_id,role,content) VALUES($1,'assistant',$2)`, chatID, reply); err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"}); return
        }

        // Periodic summarization: every 6 messages, generate/update chat summary
        go func(chatID int64) {
            cctx, ccancel := context.WithTimeout(context.Background(), 25*time.Second)
            defer ccancel()
            // count messages
            var cnt int
            _ = database.Pool.QueryRow(cctx, `SELECT COUNT(*) FROM chat_messages WHERE chat_id=$1`, chatID).Scan(&cnt)
            if cnt%6 != 0 { return }
            // fetch last 40 messages oldest-first
            rows, err := database.Pool.Query(cctx, `SELECT role, content FROM chat_messages WHERE chat_id=$1 ORDER BY id DESC LIMIT 40`, chatID)
            if err != nil { return }
            defer rows.Close()
            hist := make([][2]string, 0, 40)
            for rows.Next() {
                var role, content string
                rows.Scan(&role, &content)
                hist = append(hist, [2]string{role, content})
            }
            // reverse to oldest-first
            for i, j := 0, len(hist)-1; i<j; i,j = i+1, j-1 { hist[i], hist[j] = hist[j], hist[i] }
            // build text transcript
            var b strings.Builder
            for _, h := range hist {
                if h[0] == "assistant" { b.WriteString("Assistant: ") } else { b.WriteString("User: ") }
                b.WriteString(h[1]); b.WriteString("\n")
            }
            summPrompt := "Summarize the conversation into a concise memory for future turns. Capture key facts, decisions, figures. 120-180 words."
            ai, err := utils.NewAIClient(cctx, utils.AIConfig{APIKey: cfg.GeminiAPIKey, GenModel: cfg.GeminiModel, EmbedModel: cfg.GeminiEmbeddingModel})
            if err != nil { return }
            defer ai.Close()
            text, err := utils.GenerateText(cctx, ai, cfg.GeminiModel, genai.Text(summPrompt), genai.Text(b.String()))
            if err != nil || strings.TrimSpace(text)=="" { return }
            emb, err := utils.EmbedText(cctx, ai, cfg.GeminiEmbeddingModel, text)
            if err != nil { return }
            vec := utils.VectorLiteral(emb)
            _, _ = database.Pool.Exec(cctx,
                `INSERT INTO rag_documents(user_id, content, metadata, embedding) VALUES($1,$2,$3::jsonb, `+vec+`::vector)`,
                uid, text, `{"type":"chat_summary","chat_id":`+strconv.FormatInt(chatID,10)+`}`,
            )
        }(chatID)

        var tokensPtr *struct{
            Input  int64 `json:"input"`
            Output int64 `json:"output"`
            Total  int64 `json:"total"`
        }
        if tokIn > 0 || tokOut > 0 || tokTotal > 0 {
            tokensPtr = &struct{
                Input  int64 `json:"input"`
                Output int64 `json:"output"`
                Total  int64 `json:"total"`
            }{Input: tokIn, Output: tokOut, Total: tokTotal}
            // Update quota usage
            _, _ = database.Pool.Exec(ctx, `INSERT INTO token_quotas(user_id, token_quota, token_used, updated_at)
                VALUES($1, 50000, $2, now())
                ON CONFLICT (user_id) DO UPDATE SET token_used = token_quotas.token_used + EXCLUDED.token_used, updated_at=now()`, uid, tokTotal)
        }
        c.JSON(http.StatusOK, ChatSendResponse{ChatID: chatID, Reply: reply, RetrievedDocs: retrieved, Ingestions: ingestions, Tokens: tokensPtr})
    }
}

func retrieveRAG(ctx context.Context, aiClient interface{}, cfg config.Config, userID int64, query string) ([]string, error) {
    // compute embedding
    client, ok := aiClient.(*genai.Client)
    if !ok { return nil, fmt.Errorf("ai client type") }
    emb, err := utils.EmbedText(ctx, client, cfg.GeminiEmbeddingModel, query)
    if err != nil { return nil, err }
    vec := utils.VectorLiteral(emb)
    // nearest docs via pgvector L2 (parameterized vector)
    rows, err := database.Pool.Query(ctx,
        `SELECT content FROM rag_documents WHERE user_id=$1 ORDER BY embedding <-> $2::vector LIMIT 5`, userID, vec)
    if err != nil { return nil, err }
    defer rows.Close()
    out := []string{}
    for rows.Next() {
        var content sql.NullString
        rows.Scan(&content)
        if content.Valid { out = append(out, content.String) }
    }
    return out, nil
}

func latestChatSummary(ctx context.Context, userID, chatID int64) string {
    var s sql.NullString
    err := database.Pool.QueryRow(ctx,
        `SELECT content FROM rag_documents WHERE user_id=$1 AND metadata->>'type'='chat_summary' AND metadata->>'chat_id'=$2 ORDER BY created_at DESC LIMIT 1`,
        userID, strconv.FormatInt(chatID,10),
    ).Scan(&s)
    if err != nil { return "" }
    if s.Valid { return s.String }
    return ""
}

// -------------------- Phase 1 helpers --------------------

// processSalesHeuristic attempts to classify and process a CSV/XLSX file as sales using only heuristics and existing pipeline.
// Returns an ingestion result if successful; nil if classification failed.
func processSalesHeuristic(ctx context.Context, cfg config.Config, userID int64, filename string, content []byte, ext string) *IngestionResult {
    rows, err := readAllRows(content, ext)
    if err != nil || len(rows) == 0 { return nil }
    // Heuristic header + columns
    headerIdx, salesTxt, billTxt := heuristicDetect(rows)
    if headerIdx < 0 { return nil }
    headers := normalizeHeaders(rows, headerIdx)
    if len(headers) == 0 { return nil }
    salesCol := findColumn(headers, salesTxt)
    billCol  := findColumn(headers, billTxt)
    if salesCol == "" || billCol == "" { return nil }

    // Build records and apply cleaning
    records := buildRecords(rows, headerIdx, headers)
    preRows := len(records)
    if preRows == 0 { return nil }
    records = dropBlankRows(records)
    records, _ = dropIfSecondColumnTotalish(records, headers)
    records, _ = filterSummaryRows(records, billCol, salesCol)
    used := make([]map[string]string, 0, len(records))
    for _, r := range records {
        if !isEffectivelyEmptyBill(r[billCol]) {
            used = append(used, r)
        }
    }
    // Compute metrics
    var totalSales float64
    for _, r := range used {
        v := toNumeric(r[salesCol])
        if !math.IsNaN(v) { totalSales += v }
    }
    billRowsCount := len(used)
    if billRowsCount == 0 { return nil }
    uniqueBill := uniqueCount(used, billCol)

    // Persist metrics
    payload := map[string]any{"file_name": filename, "headers": headers}
    pb, _ := json.Marshal(payload)
    _, _ = database.Pool.Exec(ctx, `INSERT INTO sales_metrics(user_id, source_type, payload, total_sales, bill_row_count, unique_bill_count) VALUES($1,'file',$2::jsonb,$3,$4,$5)`, userID, string(pb), round2(totalSales), billRowsCount, uniqueBill)

    // Optional RAG index snapshot
    if cfg.GeminiAPIKey != "" {
        doc := "Sales metrics summary: Total sales = " + strconv.FormatFloat(round2(totalSales), 'f', 2, 64) + ", bill rows = " + strconv.Itoa(billRowsCount) + ", unique bill IDs = " + strconv.Itoa(uniqueBill)
        ai, err := utils.NewAIClient(ctx, utils.AIConfig{APIKey: cfg.GeminiAPIKey, GenModel: cfg.GeminiModel, EmbedModel: cfg.GeminiEmbeddingModel})
        if err == nil {
            emb, err := utils.EmbedText(ctx, ai, cfg.GeminiEmbeddingModel, doc)
            ai.Close()
            if err == nil {
                vec := utils.VectorLiteral(emb)
                _, _ = database.Pool.Exec(ctx, `INSERT INTO rag_documents(user_id, content, metadata, embedding) VALUES($1,$2,$3::jsonb, $4::vector)`, userID, doc, `{"source":"sales_metrics"}`, vec)
            }
        }
    }

    // Ingestion result
    met := &struct{
        TotalSales float64 `json:"total_sales"`
        BillRowCount int `json:"bill_row_count"`
        UniqueBillCount int `json:"unique_bill_count"`
    }{round2(totalSales), billRowsCount, uniqueBill}
    return &IngestionResult{Type:"sales_metrics", FileName: filename, Status:"ok", Metrics: met, Notes:"detected as sales via heuristics"}
}

// upsertKnowledgeChunks splits long text and upserts multiple chunks.
func upsertKnowledgeChunks(ctx context.Context, cfg config.Config, userID int64, filename, text string) IngestionResult {
    chunks := chunkTextLocal(text, 800)
    count := 0
    if cfg.GeminiAPIKey != "" {
        ai, err := utils.NewAIClient(ctx, utils.AIConfig{APIKey: cfg.GeminiAPIKey, GenModel: cfg.GeminiModel, EmbedModel: cfg.GeminiEmbeddingModel})
        if err == nil {
            for _, ch := range chunks {
                emb, err2 := utils.EmbedText(ctx, ai, cfg.GeminiEmbeddingModel, ch)
                if err2 != nil { continue }
                vec := utils.VectorLiteral(emb)
                _, _ = database.Pool.Exec(ctx, `INSERT INTO rag_documents(user_id, content, metadata, embedding) VALUES($1,$2,$3::jsonb, $4::vector)`, userID, ch, `{"type":"knowledge","source":"upload","file":"`+strings.ReplaceAll(filename,"\"","\"")+`"}`, vec)
                count++
            }
            ai.Close()
        }
    }
    return IngestionResult{Type:"knowledge", FileName: filename, Status:"ok", Notes:"knowledge added", ChunksIndexed:&count}
}

func chunkTextLocal(s string, size int) []string {
    s = strings.TrimSpace(s)
    if s == "" { return nil }
    if size < 200 { size = 800 }
    out := []string{}
    for len(s) > 0 {
        if len(s) <= size { out = append(out, s); break }
        cut := size
        for cut > size-160 {
            if s[cut] == ' ' || s[cut] == '\n' || s[cut] == '\t' { break }
            cut--
        }
        if cut <= 0 { cut = size }
        out = append(out, strings.TrimSpace(s[:cut]))
        s = strings.TrimSpace(s[cut:])
    }
    return out
}

// tableToText converts limited rows to a compact text for knowledge indexing.
func tableToText(rows [][]string, limit int) string {
    if limit <= 0 { limit = 200 }
    var b strings.Builder
    max := len(rows)
    if max > limit { max = limit }
    for i := 0; i < max; i++ {
        b.WriteString(strings.Join(rows[i], ", "))
        b.WriteString("\n")
    }
    return b.String()
}

// Phase 2: AI classification for sales vs knowledge based on 5-row preview
func aiClassifyIsSales(ctx context.Context, cfg config.Config, preview [][]string) (bool, float64, error) {
    if cfg.GeminiAPIKey == "" { return false, 0, fmt.Errorf("ai disabled") }
    // Build orient='split' JSON
    maxCols := 0
    for _, r := range preview { if len(r) > maxCols { maxCols = len(r) } }
    cols := make([]string, maxCols)
    for i := range cols { cols[i] = fmt.Sprintf("col%d", i) }
    split := map[string]any{"columns": cols, "index": make([]int, len(preview)), "data": preview}
    data, _ := json.Marshal(split)
    prompt := "Classify if the table is sales data.\nReturn strict JSON {\"is_sales\":true|false,\"confidence\":0..1}.\nSales data typically has a money/amount column and a bill/invoice/ref column.\nPreview (orient='split'):\n" + string(data)
    client, err := utils.NewAIClient(ctx, utils.AIConfig{APIKey: cfg.GeminiAPIKey, GenModel: cfg.GeminiModel, EmbedModel: cfg.GeminiEmbeddingModel})
    if err != nil { return false, 0, err }
    defer client.Close()
    txt, err := utils.GenerateText(ctx, client, cfg.GeminiModel, genai.Text(prompt))
    if err != nil || strings.TrimSpace(txt) == "" { return false, 0, fmt.Errorf("classification failed") }
    // strip fences if any
    t := strings.TrimSpace(txt)
    t = strings.TrimPrefix(t, "```json")
    t = strings.TrimPrefix(t, "```")
    t = strings.TrimSuffix(t, "```")
    var out struct{
        IsSales bool `json:"is_sales"`
        Conf    float64 `json:"confidence"`
    }
    if err := json.Unmarshal([]byte(t), &out); err != nil { return false, 0, err }
    return out.IsSales, out.Conf, nil
}

// Phase 2: process sales with AI header/column detection
func processSalesWithAI(ctx context.Context, cfg config.Config, userID int64, filename string, content []byte, ext string) *IngestionResult {
    if cfg.GeminiAPIKey == "" { return nil }
    rows, err := readAllRows(content, ext)
    if err != nil || len(rows) == 0 { return nil }
    preview := firstNRows(rows, 5)
    headerRowIdx, salesTxt, billTxt, aiUsed, _ := detectHeaderAndColumns(cfg, preview)
    if !aiUsed || headerRowIdx < 0 || strings.TrimSpace(salesTxt)=="" || strings.TrimSpace(billTxt)=="" { return nil }
    headers := normalizeHeaders(rows, headerRowIdx)
    salesCol := findColumn(headers, salesTxt)
    billCol  := findColumn(headers, billTxt)
    if salesCol == "" || billCol == "" { return nil }
    // cleaning + metrics
    records := buildRecords(rows, headerRowIdx, headers)
    records = dropBlankRows(records)
    records, _ = dropIfSecondColumnTotalish(records, headers)
    records, _ = filterSummaryRows(records, billCol, salesCol)
    used := make([]map[string]string, 0, len(records))
    for _, r := range records {
        if !isEffectivelyEmptyBill(r[billCol]) { used = append(used, r) }
    }
    var totalSales float64
    for _, r := range used {
        v := toNumeric(r[salesCol])
        if !math.IsNaN(v) { totalSales += v }
    }
    billRowsCount := len(used)
    if billRowsCount == 0 { return nil }
    uniqueBill := uniqueCount(used, billCol)

    payload := map[string]any{"file_name": filename, "headers": headers}
    pb, _ := json.Marshal(payload)
    _, _ = database.Pool.Exec(ctx, `INSERT INTO sales_metrics(user_id, source_type, payload, total_sales, bill_row_count, unique_bill_count) VALUES($1,'file',$2::jsonb,$3,$4,$5)`, userID, string(pb), round2(totalSales), billRowsCount, uniqueBill)

    if cfg.GeminiAPIKey != "" {
        doc := "Sales metrics summary: Total sales = " + strconv.FormatFloat(round2(totalSales), 'f', 2, 64) + ", bill rows = " + strconv.Itoa(billRowsCount) + ", unique bill IDs = " + strconv.Itoa(uniqueBill)
        ai, err := utils.NewAIClient(ctx, utils.AIConfig{APIKey: cfg.GeminiAPIKey, GenModel: cfg.GeminiModel, EmbedModel: cfg.GeminiEmbeddingModel})
        if err == nil {
            emb, err := utils.EmbedText(ctx, ai, cfg.GeminiEmbeddingModel, doc)
            ai.Close()
            if err == nil {
                vec := utils.VectorLiteral(emb)
                _, _ = database.Pool.Exec(ctx, `INSERT INTO rag_documents(user_id, content, metadata, embedding) VALUES($1,$2,$3::jsonb, $4::vector)`, userID, doc, `{"source":"sales_metrics"}`, vec)
            }
        }
    }
    met := &struct{ TotalSales float64 `json:"total_sales"`; BillRowCount int `json:"bill_row_count"`; UniqueBillCount int `json:"unique_bill_count"` }{round2(totalSales), billRowsCount, uniqueBill}
    return &IngestionResult{Type:"sales_metrics", FileName: filename, Status:"ok", Metrics: met, Notes:"detected as sales via AI"}
}

// --------- Personalization helpers (low-token summaries) ---------

func buildProfileSummary(ctx context.Context, userID int64) string {
    var name sql.NullString
    var industry, sub sql.NullString
    var emp sql.NullInt64
    var mrr, goal sql.NullFloat64
    var yrs sql.NullInt64
    err := database.Pool.QueryRow(ctx, `SELECT business_name, industry_type, sub_industry, employees, monthly_revenue, goal_amount, goal_years FROM users WHERE id=$1`, userID).
        Scan(&name, &industry, &sub, &emp, &mrr, &goal, &yrs)
    if err != nil { return "" }
    pieces := []string{}
    if name.Valid && strings.TrimSpace(name.String) != "" { pieces = append(pieces, name.String) }
    if industry.Valid {
        if sub.Valid && strings.TrimSpace(sub.String) != "" { pieces = append(pieces, industry.String+">"+sub.String) } else { pieces = append(pieces, industry.String) }
    }
    if emp.Valid && emp.Int64 > 0 { pieces = append(pieces, fmt.Sprintf("~%d employees", emp.Int64)) }
    if mrr.Valid && mrr.Float64 > 0 { pieces = append(pieces, "~"+formatK(mrr.Float64)+" MRR") }
    if goal.Valid && goal.Float64 > 0 {
        if yrs.Valid && yrs.Int64 > 0 { pieces = append(pieces, "goal "+formatK(goal.Float64)+fmt.Sprintf("/%dy", yrs.Int64)) } else { pieces = append(pieces, "goal "+formatK(goal.Float64)) }
    }
    return strings.Join(pieces, ", ")
}

func ragDocsSummary(ctx context.Context, userID int64) string {
    var total, profiles, sales int64
    _ = database.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM rag_documents WHERE user_id=$1`, userID).Scan(&total)
    _ = database.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM rag_documents WHERE user_id=$1 AND metadata->>'type'='company_profile'`, userID).Scan(&profiles)
    _ = database.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM rag_documents WHERE user_id=$1 AND metadata->>'source'='sales_metrics'`, userID).Scan(&sales)
    if total == 0 { return "" }
    var parts []string
    if profiles > 0 { parts = append(parts, "profile") }
    if sales > 0 { parts = append(parts, "sales snapshots") }
    if total-(profiles+sales) > 0 { parts = append(parts, "knowledge") }
    return "Docs: " + strings.Join(parts, ", ")
}

func formatK(v float64) string {
    if v >= 1000000 { return fmt.Sprintf("%.0fm", v/1000000) }
    if v >= 100000 { return fmt.Sprintf("%.0fk", v/1000) }
    if v >= 10000 { return fmt.Sprintf("%.0fk", v/1000) }
    if v >= 1000 { return fmt.Sprintf("%.1fk", v/1000) }
    return fmt.Sprintf("%.0f", v)
}

// --------- Additional personalization & data-query helpers ---------

// isUserDataQuery returns true if the query appears to ask about the user's stored data/profile.
var userDataQueryRe = regexp.MustCompile(`(?i)\b(what|which|tell\s*me|show|list).*(data|info|information|details|profile|records).*(about|on|for).*(me|my|profile|company)\b|\b(my\s*data|what do you know about me|what info\s*do\s*you\s*have\s*(on|about)\s*me|what.*stored\s*about\s*me)\b`)

func isUserDataQuery(q string) bool {
    return userDataQueryRe.MatchString(strings.ToLower(q))
}

// buildProfileJSON returns a compact JSON with only present fields for grounding.
func buildProfileJSON(ctx context.Context, userID int64) string {
    var name sql.NullString
    var industry, sub sql.NullString
    var emp sql.NullInt64
    var mrr, goal sql.NullFloat64
    var yrs sql.NullInt64
    err := database.Pool.QueryRow(ctx, `SELECT business_name, industry_type, sub_industry, employees, monthly_revenue, goal_amount, goal_years FROM users WHERE id=$1`, userID).
        Scan(&name, &industry, &sub, &emp, &mrr, &goal, &yrs)
    if err != nil {
        return ""
    }
    type goalT struct{ Amount float64 `json:"amount"`; Years int64 `json:"years"` }
    out := map[string]interface{}{}
    if name.Valid && strings.TrimSpace(name.String) != "" { out["business_name"] = name.String }
    if industry.Valid {
        if sub.Valid && strings.TrimSpace(sub.String) != "" { out["industry"] = industry.String+">"+sub.String } else { out["industry"] = industry.String }
    }
    if emp.Valid && emp.Int64 > 0 { out["employees"] = emp.Int64 }
    if mrr.Valid && mrr.Float64 > 0 { out["monthly_revenue"] = mrr.Float64 }
    if goal.Valid && goal.Float64 > 0 {
        if yrs.Valid && yrs.Int64 > 0 {
            out["goal"] = goalT{Amount: goal.Float64, Years: yrs.Int64}
        } else {
            out["goal"] = goalT{Amount: goal.Float64, Years: 0}
        }
    }
    if len(out) == 0 { return "" }
    b, err := json.Marshal(out)
    if err != nil { return "" }
    return string(b)
}

// latestSalesSnapshot returns a compact string of latest metrics, or empty if missing.
func latestSalesSnapshot(ctx context.Context, userID int64) string {
    var ts *float64
    var br *int
    var ub *int
    if row := database.Pool.QueryRow(ctx, `SELECT total_sales::float8, bill_row_count::int, unique_bill_count::int FROM sales_metrics WHERE user_id=$1 ORDER BY created_at DESC LIMIT 1`, userID); row != nil {
        if err := row.Scan(&ts, &br, &ub); err == nil && ts != nil && br != nil {
            return "total_sales=" + strconv.FormatFloat(*ts,'f',2,64) + ", bill_row_count=" + strconv.Itoa(*br) + func() string { if ub!=nil { return ", unique_bill_count="+strconv.Itoa(*ub) } else { return "" } }()
        }
    }
    return ""
}

// ragDocsBreakdown returns counts of user documents by category.
func ragDocsBreakdown(ctx context.Context, userID int64) string {
    var total, profiles, sales int64
    _ = database.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM rag_documents WHERE user_id=$1`, userID).Scan(&total)
    if total == 0 { return "" }
    _ = database.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM rag_documents WHERE user_id=$1 AND metadata->>'type'='company_profile'`, userID).Scan(&profiles)
    _ = database.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM rag_documents WHERE user_id=$1 AND metadata->>'source'='sales_metrics'`, userID).Scan(&sales)
    knowledge := total - (profiles + sales)
    return "docs: total=" + strconv.FormatInt(total,10) + ", profile=" + strconv.FormatInt(profiles,10) + ", sales=" + strconv.FormatInt(sales,10) + func() string { if knowledge>0 { return ", knowledge="+strconv.FormatInt(knowledge,10) } else { return "" } }()
}
