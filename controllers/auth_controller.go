package controllers

import (
    "bytes"
    "context"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "io"
    "log"
    "net/http"
    "time"

    "github.com/gin-gonic/gin"
    "scalingwolf-ai/backend/config"
    "scalingwolf-ai/backend/database"
    "scalingwolf-ai/backend/models"
    "scalingwolf-ai/backend/utils"
)

func hash(pw string) string {
    h := sha256.Sum256([]byte(pw))
    return hex.EncodeToString(h[:])
}

func RequestOTP(cfg config.Config) gin.HandlerFunc {
    return func(c *gin.Context) {
        var req models.OTPRequest
        if err := c.ShouldBindJSON(&req); err != nil {
            c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
            return
        }

        jsonData, _ := json.Marshal(req)
        resp, err := http.Post(cfg.OTPRequestURL, "application/json", bytes.NewBuffer(jsonData))
        if err != nil {
            c.JSON(http.StatusBadGateway, gin.H{"error": "otp provider error"})
            return
        }
        defer resp.Body.Close()

        body, _ := io.ReadAll(resp.Body)
        log.Printf("OTP provider response (%d): %s", resp.StatusCode, string(body))
        c.Data(resp.StatusCode, "application/json", body)
    }
}

func VerifyOTP(cfg config.Config) gin.HandlerFunc {
    return func(c *gin.Context) {
        var req models.OTPVerifyRequest
        if err := c.ShouldBindJSON(&req); err != nil {
            c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
            return
        }

        jsonData, _ := json.Marshal(req)
        resp, err := http.Post(cfg.OTPVerifyURL, "application/json", bytes.NewBuffer(jsonData))
        if err != nil {
            c.JSON(http.StatusBadGateway, gin.H{"error": "otp provider error"})
            return
        }
        defer resp.Body.Close()

        body, _ := io.ReadAll(resp.Body)
        log.Printf("OTP verify response (%d): %s", resp.StatusCode, string(body))

        if resp.StatusCode != http.StatusOK {
            c.Data(resp.StatusCode, "application/json", body)
            return
        }

        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        _, err = database.Pool.Exec(ctx, `UPDATE users SET is_whatsapp_verified = TRUE WHERE phone = $1`, req.Phone)
        if err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
            return
        }
        c.JSON(http.StatusOK, gin.H{"status": "verified"})
    }
}

func Register(cfg config.Config) gin.HandlerFunc {
    return func(c *gin.Context) {
        var req models.RegisterRequest
        if err := c.ShouldBindJSON(&req); err != nil {
            c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
            return
        }
        if req.Password == "" || req.Password != req.Confirm {
            c.JSON(http.StatusBadRequest, gin.H{"error": "password mismatch"})
            return
        }
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        // Ensure OTP verified first (only block if a record exists and not verified)
        var verified bool
        err := database.Pool.QueryRow(ctx, `SELECT COALESCE(is_whatsapp_verified,false) FROM users WHERE phone=$1`, req.Phone).Scan(&verified)
        if err == nil && !verified {
            c.JSON(http.StatusForbidden, gin.H{"error": "whatsapp not verified"})
            return
        }
        // Upsert user (no business_name at registration)
        var id int64
        err = database.Pool.QueryRow(ctx, `INSERT INTO users(name,email,password_hash,phone,is_whatsapp_verified)
VALUES($1,$2,$3,$4,TRUE)
ON CONFLICT (email) DO UPDATE SET name=EXCLUDED.name, password_hash=EXCLUDED.password_hash, phone=EXCLUDED.phone
RETURNING id`, req.Name, req.Email, hash(req.Password), req.Phone).Scan(&id)
        if err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
            return
        }
        token, _ := utils.GenerateJWT(cfg.JWTSecret, id, 24*time.Hour)
        c.JSON(http.StatusOK, gin.H{"token": token})
    }
}

func Login(cfg config.Config) gin.HandlerFunc {
    return func(c *gin.Context) {
        var req models.LoginRequest
        if err := c.ShouldBindJSON(&req); err != nil {
            c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
            return
        }
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        var id int64
        var pw string
        err := database.Pool.QueryRow(ctx, `SELECT id, password_hash FROM users WHERE email=$1`, req.Email).Scan(&id, &pw)
        if err != nil || pw != hash(req.Password) {
            c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
            return
        }
        token, _ := utils.GenerateJWT(cfg.JWTSecret, id, 24*time.Hour)
        c.JSON(http.StatusOK, gin.H{"token": token})
    }
}

func CompanySetup(cfg config.Config) gin.HandlerFunc {
    return func(c *gin.Context) {
        var req models.CompanySetupRequest
        if err := c.ShouldBindJSON(&req); err != nil {
            c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
            return
        }
        uid := c.GetInt64("user_id")
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        _, err := database.Pool.Exec(ctx, `UPDATE users SET business_name=$1, monthly_revenue=$2, employees=$3, goal_amount=$4, goal_years=$5, industry_type=$6, sub_industry=$7, core_processes=$8 WHERE id=$9`,
            req.BusinessName, req.MonthlyRevenue, req.Employees, req.GoalAmount, req.GoalYears, req.IndustryType, req.SubIndustry, req.CoreProcesses, uid)
        if err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
            return
        }
        c.JSON(http.StatusOK, gin.H{"status": "ok"})
    }
}
