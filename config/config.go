package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
    Port          string
    DatabaseURL   string // Supabase Postgres connection string
    JWTSecret     string
    OTPRequestURL string
    OTPVerifyURL  string
    GeminiAPIKey  string
    GeminiModel   string
    GeminiEmbeddingModel string
}

func Load() Config {
    _ = godotenv.Load()
    cfg := Config{
        Port:          get("PORT", "8080"),
        DatabaseURL:   must("SUPABASE_DB_URL"),
        JWTSecret:     must("JWT_SECRET"),
        OTPRequestURL: get("OTP_REQUEST_URL", "https://scalingwolf.ai/loginpage/request-otp"),
        OTPVerifyURL:  get("OTP_VERIFY_URL", "https://scalingwolf.ai/loginpage/verify-otp"),
        GeminiAPIKey:  get("GEMINI_API_KEY", ""),
        GeminiModel:   get("GEMINI_MODEL", "gemini-2.5-pro"),
        GeminiEmbeddingModel: get("GEMINI_EMBEDDING_MODEL", "text-embedding-004"),
    }
    return cfg
}

func get(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func must(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("missing required env: %s", k)
	}
	return v
}
