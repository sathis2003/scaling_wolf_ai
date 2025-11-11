package controllers

import (
    "context"
    "encoding/json"
    "net/http"
    "regexp"
    "strconv"
    "strings"
    "time"

    "github.com/gin-gonic/gin"
    "scalingwolf-ai/backend/config"
    "scalingwolf-ai/backend/database"
)

type SalesTextRequest struct {
    TotalSales      *float64 `json:"total_sales"`
    BillRowCount    *int     `json:"bill_row_count"`
    UniqueBillCount *int     `json:"unique_bill_count"`
    Text            string   `json:"text"`
}

func IngestSalesText(cfg config.Config) gin.HandlerFunc {
    return func(c *gin.Context) {
        var req SalesTextRequest
        if err := c.ShouldBindJSON(&req); err != nil {
            c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
            return
        }
        uid := c.GetInt64("user_id")

        total, rows, uniq := detectSalesFromRequest(req)
        payload := map[string]any{
            "source": "text",
            "raw_text": req.Text,
        }
        pb, _ := json.Marshal(payload)
        ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
        defer cancel()
        _, err := database.Pool.Exec(ctx, `INSERT INTO sales_metrics(user_id, source_type, payload, total_sales, bill_row_count, unique_bill_count) VALUES($1,'text',$2::jsonb,$3,$4,$5)`, uid, string(pb), total, rows, uniq)
        if err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error": "db insert error"}); return }
        c.JSON(http.StatusOK, gin.H{"status":"ok", "metrics": gin.H{"total_sales": total, "bill_row_count": rows, "unique_bill_count": uniq}})
    }
}

type SalesMetric struct {
    ID              int64           `json:"id"`
    SourceType      string          `json:"source_type"`
    Payload         json.RawMessage `json:"payload"`
    TotalSales      *float64        `json:"total_sales"`
    BillRowCount    *int            `json:"bill_row_count"`
    UniqueBillCount *int            `json:"unique_bill_count"`
    CreatedAt       time.Time       `json:"created_at"`
}

// ListSalesMetrics returns paginated sales metrics for the authenticated user
func ListSalesMetrics() gin.HandlerFunc {
    return func(c *gin.Context) {
        uid := c.GetInt64("user_id")
        limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
        offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
        if limit <= 0 || limit > 100 { limit = 20 }
        if offset < 0 { offset = 0 }
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        rows, err := database.Pool.Query(ctx, `
            SELECT id, source_type, payload::text, total_sales::float8, bill_row_count::int, unique_bill_count::int, created_at
            FROM sales_metrics WHERE user_id=$1
            ORDER BY created_at DESC
            LIMIT $2 OFFSET $3`, uid, limit, offset)
        if err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error":"db error"}); return }
        defer rows.Close()
        out := []SalesMetric{}
        for rows.Next() {
            var m SalesMetric
            var payloadText string
            rows.Scan(&m.ID, &m.SourceType, &payloadText, &m.TotalSales, &m.BillRowCount, &m.UniqueBillCount, &m.CreatedAt)
            m.Payload = json.RawMessage(payloadText)
            out = append(out, m)
        }
        c.JSON(http.StatusOK, gin.H{"items": out, "limit": limit, "offset": offset})
    }
}

// GetSalesMetric returns a single sales metric by id for the authenticated user
func GetSalesMetric() gin.HandlerFunc {
    return func(c *gin.Context) {
        uid := c.GetInt64("user_id")
        idStr := c.Param("id")
        id, _ := strconv.ParseInt(idStr, 10, 64)
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        var m SalesMetric
        var payloadText string
        err := database.Pool.QueryRow(ctx, `
            SELECT id, source_type, payload::text, total_sales::float8, bill_row_count::int, unique_bill_count::int, created_at
            FROM sales_metrics WHERE id=$1 AND user_id=$2`, id, uid,
        ).Scan(&m.ID, &m.SourceType, &payloadText, &m.TotalSales, &m.BillRowCount, &m.UniqueBillCount, &m.CreatedAt)
        if err != nil { c.JSON(http.StatusNotFound, gin.H{"error":"not found"}); return }
        m.Payload = json.RawMessage(payloadText)
        c.JSON(http.StatusOK, m)
    }
}

// GetLatestSalesMetric returns the most recent sales metric for the authenticated user
func GetLatestSalesMetric() gin.HandlerFunc {
    return func(c *gin.Context) {
        uid := c.GetInt64("user_id")
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        var m SalesMetric
        var payloadText string
        err := database.Pool.QueryRow(ctx, `
            SELECT id, source_type, payload::text, total_sales::float8, bill_row_count::int, unique_bill_count::int, created_at
            FROM sales_metrics WHERE user_id=$1 ORDER BY created_at DESC LIMIT 1`, uid,
        ).Scan(&m.ID, &m.SourceType, &payloadText, &m.TotalSales, &m.BillRowCount, &m.UniqueBillCount, &m.CreatedAt)
        if err != nil { c.JSON(http.StatusNotFound, gin.H{"error":"no sales metrics"}); return }
        m.Payload = json.RawMessage(payloadText)
        c.JSON(http.StatusOK, m)
    }
}

func detectSalesFromRequest(req SalesTextRequest) (float64, int, int) {
    if req.TotalSales != nil && req.BillRowCount != nil && req.UniqueBillCount != nil {
        return *req.TotalSales, *req.BillRowCount, *req.UniqueBillCount
    }
    // heuristics from free text: look for amounts with currency-like tokens and counts
    t := strings.ToLower(req.Text)
    amount := extractFirstFloat(t)
    counts := extractAllInts(t)
    billRows, uniq := 0, 0
    if len(counts) > 0 { billRows = counts[0] }
    if len(counts) > 1 { uniq = counts[1] }
    return amount, billRows, uniq
}

var floatRe = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)`)
var intRe = regexp.MustCompile(`\b([0-9]{1,9})\b`)

func extractFirstFloat(s string) float64 {
    m := floatRe.FindStringSubmatch(s)
    if len(m) < 2 { return 0 }
    f, _ := strconv.ParseFloat(m[1], 64)
    return f
}

func extractAllInts(s string) []int {
    ms := intRe.FindAllStringSubmatch(s, -1)
    out := []int{}
    for _, m := range ms {
        if len(m) < 2 { continue }
        v, err := strconv.Atoi(m[1])
        if err == nil { out = append(out, v) }
    }
    return out
}
