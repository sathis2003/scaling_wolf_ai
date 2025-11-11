package controllers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"scalingwolf-ai/backend/database"
	"scalingwolf-ai/backend/models"
)

func Me() gin.HandlerFunc {
    return func(c *gin.Context) {
        uid := c.GetInt64("user_id")
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        var u models.User
        err := database.Pool.QueryRow(ctx, `SELECT id,name,business_name,email,phone,is_whatsapp_verified,industry_type,sub_industry,COALESCE(core_processes,'{}'::text[])::text[], monthly_revenue, employees, goal_amount, goal_years, created_at FROM users WHERE id=$1`, uid).
            Scan(&u.ID, &u.Name, &u.BusinessName, &u.Email, &u.Phone, &u.IsWhatsAppVerified, &u.IndustryType, &u.SubIndustry, &u.CoreProcesses, &u.MonthlyRevenue, &u.Employees, &u.GoalAmount, &u.GoalYears, &u.CreatedAt)
        if err != nil {
            c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
            return
        }
        c.JSON(http.StatusOK, u)
    }
}
