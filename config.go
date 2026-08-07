package main

import (
	"log"
	"os"
	"strconv"
)

// ==========================================
// ⚙️ 設定項目（環境変数から取得、未設定時はデフォルト値）
// ==========================================
var (
	MinifluxURL    = getEnv("MINIFLUX_URL", "https://miniflux.example.com")
	MinifluxAPIKey = getEnv("MINIFLUX_API_KEY", "")
	AIFeedID       = getEnvInt64("AI_FEED_ID", 1)
	TargetFeedID   = getEnvInt64("TARGET_FEED_ID", 2)

	DiscordWebhookURL = getEnv("DISCORD_WEBHOOK_URL", "")

	LLMServerURL   = getEnv("LLM_SERVER_URL", "http://192.168.0.14:8000/v1/chat/completions")
	LLMModelName   = getEnv("LLM_MODEL_NAME", "Gemma-4-E2B-it")
	LLMServerToken = getEnv("LLM_SERVER_TOKEN", "")
	ScoreThreshold = getEnvInt("SCORE_THRESHOLD", 5)
	LLMRetryCount  = getEnvInt("LLM_RETRY_COUNT", 2)
	SystemPrompt   = getEnv("SYSTEM_PROMPT", `あなたは優秀なニュースキュレーターです。"rkun"という人物のためにニュースを選別して届けます。`)

	DigestCron           = getEnv("DIGEST_CRON", "10 * * * *")
	DigestScoreThreshold = getEnvInt64("DIGEST_SCORE_THRESHOLD", 8)
)

// ------------------------------------------
// 🛠️ 環境変数取得用のヘルパー関数
// ------------------------------------------

// 文字列の環境変数を取得（未設定時はデフォルト値を返す）
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

// int64型の環境変数を取得
func getEnvInt64(key string, fallback int64) int64 {
	valueStr, exists := os.LookupEnv(key)
	if !exists {
		return fallback
	}
	value, err := strconv.ParseInt(valueStr, 10, 64)
	if err != nil {
		log.Printf("[Warning] 環境変数 %s のパースに失敗しました（値: %s）。デフォルト値 %d を使用します。", key, valueStr, fallback)
		return fallback
	}
	return value
}

// int型の環境変数を取得
func getEnvInt(key string, fallback int) int {
	valueStr, exists := os.LookupEnv(key)
	if !exists {
		return fallback
	}
	value, err := strconv.Atoi(valueStr)
	if err != nil {
		log.Printf("[Warning] 環境変数 %s のパースに失敗しました（値: %s）。デフォルト値 %d を使用します。", key, valueStr, fallback)
		return fallback
	}
	return value
}
