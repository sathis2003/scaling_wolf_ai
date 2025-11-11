package middlewares

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"scalingwolf-ai/backend/utils"
)

func Auth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}
		t := strings.TrimPrefix(h, "Bearer ")
		claims, err := utils.ParseJWT(secret, t)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Set("user_id", claims.UserID)
		c.Next()
	}
}
