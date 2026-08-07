package main

// ==========================================
// 📦 各種APIとやり取りするための構造体定義
// ==========================================

// MinifluxのWebhookから届くペイロードの構造
type MinifluxWebhook struct {
	EventType string          `json:"event_type"`
	Entries   []MinifluxEntry `json:"entries"`
}

type MinifluxEntry struct {
	ID      int64  `json:"id"`
	FeedID  int64  `json:"feed_id"`
	Title   string `json:"title"`
	Content string `json:"content"`
	URL     string `json:"url"`
}

// ローカルLLM（OpenAI互換）へのリクエスト/レスポンス構造
type LLMRequest struct {
	Model       string       `json:"model"`
	Messages    []LLMMessage `json:"messages"`
	Temperature float64      `json:"temperature"`
}

type LLMMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type LLMResponse struct {
	Choices []struct {
		Message LLMMessage `json:"message"`
	} `json:"choices"`
}

// ローカルLLMに期待する出力JSON構造
type LLMOutput struct {
	Score     int    `json:"score"`
	TitleJa   string `json:"title_ja"`
	SummaryJa string `json:"summary_ja"`
	Comment   string `json:"comment"`
}

// Minifluxへのインサート用構造
type MinifluxInsertPayload struct {
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Author      string   `json:"author"`
	Content     string   `json:"content"`
	PublishedAt int64    `json:"published_at"`
	Status      string   `json:"status"`
	Tags        []string `json:"tags"`
}

// ==========================================
// 📦 Discord通知用の構造体定義
// ==========================================
type DiscordPayload struct {
	Embeds []DiscordEmbed `json:"embeds"`
}

type DiscordEmbed struct {
	Title       string         `json:"title"`
	Description string         `json:"description"`
	URL         string         `json:"url"`
	Color       int            `json:"color"`
	Fields      []DiscordField `json:"fields"`
	Timestamp   string         `json:"timestamp"`
}

type DiscordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}