package routes

import (
	"github.com/gin-gonic/gin"
	"scalingwolf-ai/backend/config"
	"scalingwolf-ai/backend/controllers"
	"scalingwolf-ai/backend/middlewares"
)

func Register(r *gin.Engine, cfg config.Config) {
    api := r.Group("/api")
    {
        auth := api.Group("/auth")
        auth.POST("/request-otp", controllers.RequestOTP(cfg))
        auth.POST("/verify-otp", controllers.VerifyOTP(cfg))
        auth.POST("/register", controllers.Register(cfg))
        auth.POST("/login", controllers.Login(cfg))

        priv := api.Group("/")
        priv.Use(middlewares.Auth(cfg.JWTSecret))
        priv.POST("company/setup", controllers.CompanySetup(cfg))
        priv.GET("me", controllers.Me())
        // Upload and analyze sales/bill file (CSV/XLSX)
        priv.POST("data/upload-analyze", controllers.UploadAnalyze(cfg))
        // Sales metrics via text
        priv.POST("data/sales-text", controllers.IngestSalesText(cfg))
        // Fetch sales metrics (list + single)
        priv.GET("data/sales", controllers.ListSalesMetrics())
        priv.GET("data/sales/latest", controllers.GetLatestSalesMetric())
        priv.GET("data/sales/:id", controllers.GetSalesMetric())
        // BEP calculation using latest metrics or overrides
        priv.POST("data/bep/calc", controllers.CalcBEP(cfg))
        priv.GET("data/bep/latest", controllers.GetLatestBEP())
        // RAG: upsert text document
        priv.POST("rag/upsert-text", controllers.RAGUpsertText(cfg))
        // RAG: upsert chunked long text and search
        priv.POST("rag/upsert-chunks", controllers.RAGUpsertChunks(cfg))
        priv.POST("rag/search", controllers.RAGSearch(cfg))
        // Chat: send message (creates chat if needed)
        priv.POST("chat/send", controllers.ChatSend(cfg))
        // Chat management
        priv.POST("chat/new", controllers.ChatCreate())
        priv.GET("chat", controllers.ChatList())
        priv.GET("chat/:id/messages", controllers.ChatGetMessages())
        priv.PUT("chat/:id/title", controllers.ChatRename())
        priv.DELETE("chat/:id", controllers.ChatDelete())
        // Token quotas
        priv.GET("tokens/usage", controllers.TokensUsage())
        priv.POST("tokens/set-plan", controllers.TokensSetPlan())
    }
}
