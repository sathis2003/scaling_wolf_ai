package controllers

import (
    "context"
    "net/http"
    "time"

    "github.com/gin-gonic/gin"
    "scalingwolf-ai/backend/database"
)

type SetPlanRequest struct {
    Points    int  `json:"points"`          // 1 point = 10,000 tokens; max 5 points
    ResetUsed bool `json:"reset_used"`      // optional: reset usage to 0
}

func TokensUsage() gin.HandlerFunc {
    return func(c *gin.Context) {
        uid := c.GetInt64("user_id")
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        var quota, used int64
        err := database.Pool.QueryRow(ctx, `SELECT token_quota::bigint, token_used::bigint FROM token_quotas WHERE user_id=$1`, uid).Scan(&quota, &used)
        if err != nil {
            // default: 5 points = 50k
            quota = 50000
            used = 0
        }
        remaining := quota - used
        if remaining < 0 { remaining = 0 }
        points := quota / 10000
        pointsUsed := used / 10000
        pointsRemaining := remaining / 10000
        c.JSON(http.StatusOK, gin.H{
            "points": points,
            "token_quota": quota,
            "token_used": used,
            "remaining": remaining,
            "points_used": pointsUsed,
            "points_remaining": pointsRemaining,
        })
    }
}

func TokensSetPlan() gin.HandlerFunc {
    return func(c *gin.Context) {
        uid := c.GetInt64("user_id")
        var req SetPlanRequest
        if err := c.ShouldBindJSON(&req); err != nil || req.Points <= 0 {
            c.JSON(http.StatusBadRequest, gin.H{"error":"invalid body"}); return
        }
        if req.Points > 5 { req.Points = 5 }
        quota := int64(req.Points) * 10000
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        if req.ResetUsed {
            _, err := database.Pool.Exec(ctx, `INSERT INTO token_quotas(user_id, token_quota, token_used, updated_at)
                VALUES($1,$2,0,now())
                ON CONFLICT (user_id) DO UPDATE SET token_quota=EXCLUDED.token_quota, token_used=0, updated_at=now()`, uid, quota)
            if err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error":"db error"}); return }
        } else {
            _, err := database.Pool.Exec(ctx, `INSERT INTO token_quotas(user_id, token_quota, updated_at)
                VALUES($1,$2,now())
                ON CONFLICT (user_id) DO UPDATE SET token_quota=EXCLUDED.token_quota, updated_at=now()`, uid, quota)
            if err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error":"db error"}); return }
        }
        c.JSON(http.StatusOK, gin.H{"status":"ok", "points": req.Points, "token_quota": quota})
    }
}

