package controllers

import (
    "context"
    "encoding/json"
    "net/http"
    "time"
    "strings"
    "log"

    "github.com/gin-gonic/gin"
    "scalingwolf-ai/backend/config"
    "scalingwolf-ai/backend/database"
    "scalingwolf-ai/backend/utils"
)

type RAGUpsertTextRequest struct {
    Text     string                 `json:"text"`
    Metadata map[string]any         `json:"metadata"`
}

func RAGUpsertText(cfg config.Config) gin.HandlerFunc {
    return func(c *gin.Context) {
        var req RAGUpsertTextRequest
        if err := c.ShouldBindJSON(&req); err != nil || req.Text == "" {
            c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body or missing text"})
            return
        }
        uid := c.GetInt64("user_id")
        ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
        defer cancel()

        aiClient, err := utils.NewAIClient(ctx, utils.AIConfig{APIKey: cfg.GeminiAPIKey, GenModel: cfg.GeminiModel, EmbedModel: cfg.GeminiEmbeddingModel})
        if err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "ai client error"})
            return
        }
        defer aiClient.Close()

        emb, err := utils.EmbedText(ctx, aiClient, cfg.GeminiEmbeddingModel, req.Text)
        if err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "embedding failed"})
            return
        }
        meta := req.Metadata
        if meta == nil { meta = map[string]any{} }
        mb, _ := json.Marshal(meta)

        // Insert with vector literal cast
        vec := utils.VectorLiteral(emb)
        _, err = database.Pool.Exec(ctx,
            `INSERT INTO rag_documents(user_id, content, metadata, embedding) VALUES ($1, $2, $3::jsonb, $4::vector)`,
            uid, req.Text, string(mb), vec,
        )
        if err != nil {
            log.Printf("rag upsert insert error: %v", err)
            c.JSON(http.StatusInternalServerError, gin.H{"error": "db insert error"})
            return
        }
        c.JSON(http.StatusOK, gin.H{"status": "ok"})
    }
}

type RAGSearchRequest struct {
    Query string `json:"query"`
    K     int    `json:"k"`
}

func RAGSearch(cfg config.Config) gin.HandlerFunc {
    return func(c *gin.Context) {
        var req RAGSearchRequest
        if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Query)=="" {
            c.JSON(http.StatusBadRequest, gin.H{"error":"invalid body or missing query"}); return
        }
        if req.K <= 0 || req.K > 10 { req.K = 5 }
        uid := c.GetInt64("user_id")
        ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
        defer cancel()
        aiClient, err := utils.NewAIClient(ctx, utils.AIConfig{APIKey: cfg.GeminiAPIKey, GenModel: cfg.GeminiModel, EmbedModel: cfg.GeminiEmbeddingModel})
        if err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error":"ai client error"}); return }
        defer aiClient.Close()
        emb, err := utils.EmbedText(ctx, aiClient, cfg.GeminiEmbeddingModel, req.Query)
        if err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error":"embedding failed"}); return }
        vec := utils.VectorLiteral(emb)
        rows, err := database.Pool.Query(ctx, `SELECT content FROM rag_documents WHERE user_id=$1 ORDER BY embedding <-> $2::vector LIMIT $3`, uid, vec, req.K)
        if err != nil { log.Printf("rag search query error: %v", err); c.JSON(http.StatusInternalServerError, gin.H{"error":"db error"}); return }
        defer rows.Close()
        docs := []string{}
        for rows.Next() { var s string; rows.Scan(&s); docs = append(docs, s) }
        c.JSON(http.StatusOK, gin.H{"docs": docs})
    }
}

type RAGUpsertChunksRequest struct {
    Text     string         `json:"text"`
    Metadata map[string]any `json:"metadata"`
    ChunkSize int           `json:"chunk_size"`
}

func RAGUpsertChunks(cfg config.Config) gin.HandlerFunc {
    return func(c *gin.Context) {
        var req RAGUpsertChunksRequest
        if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Text)=="" {
            c.JSON(http.StatusBadRequest, gin.H{"error":"invalid body or missing text"}); return
        }
        uid := c.GetInt64("user_id")
        size := req.ChunkSize
        if size < 400 || size > 1600 { size = 800 }
        chunks := chunkText(req.Text, size)
        ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
        defer cancel()
        aiClient, err := utils.NewAIClient(ctx, utils.AIConfig{APIKey: cfg.GeminiAPIKey, GenModel: cfg.GeminiModel, EmbedModel: cfg.GeminiEmbeddingModel})
        if err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error":"ai client error"}); return }
        defer aiClient.Close()
        meta := req.Metadata
        if meta == nil { meta = map[string]any{} }
        mb, _ := json.Marshal(meta)
        for _, ch := range chunks {
            emb, err := utils.EmbedText(ctx, aiClient, cfg.GeminiEmbeddingModel, ch)
            if err != nil { continue }
            vec := utils.VectorLiteral(emb)
            _, _ = database.Pool.Exec(ctx, `INSERT INTO rag_documents(user_id, content, metadata, embedding) VALUES($1,$2,$3::jsonb, $4::vector)`, uid, ch, string(mb), vec)
        }
        c.JSON(http.StatusOK, gin.H{"status":"ok", "chunks": len(chunks)})
    }
}

func chunkText(s string, size int) []string {
    s = strings.TrimSpace(s)
    if s == "" { return nil }
    out := []string{}
    for len(s) > 0 {
        if len(s) <= size { out = append(out, s); break }
        cut := size
        // try to cut at whitespace
        for cut > size-160 { // allow 20% backtrack
            if s[cut] == ' ' || s[cut] == '\n' || s[cut] == '\t' { break }
            cut--
        }
        if cut <= 0 { cut = size }
        out = append(out, strings.TrimSpace(s[:cut]))
        s = strings.TrimSpace(s[cut:])
    }
    return out
}
