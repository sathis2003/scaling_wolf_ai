package main

import (
	"github.com/gin-gonic/gin"
	"log"
	"scalingwolf-ai/backend/config"
	"scalingwolf-ai/backend/database"
	"scalingwolf-ai/backend/routes"
)

func main() {
    cfg := config.Load()
    database.Connect(cfg.DatabaseURL)
    database.EnsureSchema()
    r := gin.Default()
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})
	routes.Register(r, cfg)
	log.Printf("server on :%s", cfg.Port)
	r.Run(":" + cfg.Port)
}
