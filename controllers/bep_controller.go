package controllers

import (
    "context"
    "math"
    "net/http"
    "time"

    "github.com/gin-gonic/gin"
    "scalingwolf-ai/backend/config"
    "scalingwolf-ai/backend/database"
)

type CalcBEPRequest struct {
    // Costs and margins (provide at least one of the margin inputs)
    FixedCost            float64  `json:"fixed_cost"`                         // required
    VariableCostRate     *float64 `json:"variable_cost_rate,omitempty"`       // e.g., 0.6 means 60% of revenue is variable cost
    VariableCostPerBill  *float64 `json:"variable_cost_per_bill,omitempty"`   // absolute cost per bill/invoice
    GrossMarginRate      *float64 `json:"gross_margin_rate,omitempty"`        // e.g., 0.4 means 40% gross margin

    // Optional overrides for metrics; otherwise latest metrics are used
    TotalSalesOverride   *float64 `json:"total_sales,omitempty"`
    BillRowCountOverride *int     `json:"bill_row_count,omitempty"`
}

func CalcBEP(cfg config.Config) gin.HandlerFunc {
    return func(c *gin.Context) {
        var req CalcBEPRequest
        if err := c.ShouldBindJSON(&req); err != nil || req.FixedCost <= 0 {
            c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body or fixed_cost"})
            return
        }
        uid := c.GetInt64("user_id")

        // Resolve metrics: from overrides or latest sales_metrics
        var totalSales float64
        var billRows int
        var sourceMetricsID *int64
        if req.TotalSalesOverride != nil && req.BillRowCountOverride != nil {
            totalSales = *req.TotalSalesOverride
            billRows = *req.BillRowCountOverride
        } else {
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            defer cancel()
            var id int64
            var ts *float64
            var br *int
            err := database.Pool.QueryRow(ctx,
                `SELECT id, total_sales::float8, bill_row_count::int FROM sales_metrics WHERE user_id=$1 AND total_sales IS NOT NULL AND bill_row_count IS NOT NULL AND bill_row_count > 0 ORDER BY created_at DESC LIMIT 1`,
                uid,
            ).Scan(&id, &ts, &br)
            if err != nil || ts == nil || br == nil || *br == 0 {
                c.JSON(http.StatusBadRequest, gin.H{"error": "no sales metrics found; upload a file or provide overrides"})
                return
            }
            totalSales = *ts
            billRows = *br
            sourceMetricsID = &id
        }
        if billRows <= 0 {
            c.JSON(http.StatusBadRequest, gin.H{"error": "bill_row_count must be > 0"})
            return
        }
        avgRevenue := totalSales / float64(billRows)

        // Determine contribution per bill
        var contrib float64
        if req.VariableCostPerBill != nil {
            contrib = avgRevenue - *req.VariableCostPerBill
        } else if req.VariableCostRate != nil {
            r := *req.VariableCostRate
            if r < 0 || r > 1 { c.JSON(http.StatusBadRequest, gin.H{"error": "variable_cost_rate must be between 0 and 1"}); return }
            contrib = avgRevenue * (1 - r)
        } else if req.GrossMarginRate != nil {
            g := *req.GrossMarginRate
            if g < 0 || g > 1 { c.JSON(http.StatusBadRequest, gin.H{"error": "gross_margin_rate must be between 0 and 1"}); return }
            contrib = avgRevenue * g
        } else {
            c.JSON(http.StatusBadRequest, gin.H{"error": "provide variable_cost_per_bill or variable_cost_rate or gross_margin_rate"})
            return
        }
        if !(contrib > 0) {
            c.JSON(http.StatusBadRequest, gin.H{"error": "contribution per bill must be > 0"})
            return
        }

        bepBills := int(math.Ceil(req.FixedCost / contrib))
        bepSales := float64(bepBills) * avgRevenue

        // Persist result
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        var srcID any
        if sourceMetricsID != nil { srcID = *sourceMetricsID } else { srcID = nil }
        _, err := database.Pool.Exec(ctx, `
            INSERT INTO bep_results(user_id, source_metrics_id, fixed_cost, variable_cost_rate, variable_cost_per_bill, gross_margin_rate, avg_revenue_per_bill, contribution_per_bill, bep_bills, bep_sales)
            VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
        `, uid, srcID, req.FixedCost, req.VariableCostRate, req.VariableCostPerBill, req.GrossMarginRate, avgRevenue, contrib, bepBills, bepSales)
        if err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "db insert error"})
            return
        }

        c.JSON(http.StatusOK, gin.H{
            "bep": gin.H{"bills": bepBills, "sales": bepSales},
            "metrics_used": gin.H{"total_sales": totalSales, "bill_row_count": billRows, "source_metrics_id": sourceMetricsID},
            "derived": gin.H{"avg_revenue_per_bill": avgRevenue, "contribution_per_bill": contrib},
        })
    }
}

func GetLatestBEP() gin.HandlerFunc {
    return func(c *gin.Context) {
        uid := c.GetInt64("user_id")
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        var (
            bepBills int
            bepSales float64
            avgRev float64
            contrib float64
            fixed float64
            vRate *float64
            vPerBill *float64
            gRate *float64
            created time.Time
        )
        err := database.Pool.QueryRow(ctx, `
            SELECT bep_bills::int, bep_sales::float8, avg_revenue_per_bill::float8, contribution_per_bill::float8, fixed_cost::float8, variable_cost_rate::float8, variable_cost_per_bill::float8, gross_margin_rate::float8, created_at
            FROM bep_results WHERE user_id=$1 ORDER BY created_at DESC LIMIT 1`, uid,
        ).Scan(&bepBills, &bepSales, &avgRev, &contrib, &fixed, &vRate, &vPerBill, &gRate, &created)
        if err != nil {
            c.JSON(http.StatusNotFound, gin.H{"error": "no bep results"})
            return
        }
        c.JSON(http.StatusOK, gin.H{
            "bep": gin.H{"bills": bepBills, "sales": bepSales},
            "derived": gin.H{"avg_revenue_per_bill": avgRev, "contribution_per_bill": contrib},
            "inputs": gin.H{"fixed_cost": fixed, "variable_cost_rate": vRate, "variable_cost_per_bill": vPerBill, "gross_margin_rate": gRate},
            "created_at": created,
        })
    }
}
