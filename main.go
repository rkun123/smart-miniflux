package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
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
	ScoreThreshold = getEnvInt("SCORE_THRESHOLD", 5)
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
	Color       int            `json:"color"` // 10進数のカラーコード
	Fields      []DiscordField `json:"fields"`
	Timestamp   string         `json:"timestamp"`
}

type DiscordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

// ==========================================
// 🚀 起動時に最新1件をチェックする関数
// ==========================================
func checkLatestEntryOnStart() {
	log.Printf("[StartCheck] フィードID: %d の最新1件をチェック中...", TargetFeedID)

	client := &http.Client{Timeout: 10 * time.Second}

	// 特定のフィードの記事一覧を、最新順(direction=desc)、1件だけ(limit=1)取得するAPI URL
	url := fmt.Sprintf("%s/v1/feeds/%d/entries?direction=desc&limit=1", MinifluxURL, TargetFeedID)

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("X-Auth-Token", MinifluxAPIKey)

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[StartCheck] Miniflux APIへの接続に失敗: %v", err)
		return
	}
	defer resp.Body.Close()

	// Minifluxの通常のエントリ一覧レスポンスをパースする構造体
	var result struct {
		Entries []MinifluxEntry `json:"entries"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("[StartCheck] レスポンスのパースに失敗: %v", err)
		return
	}

	if len(result.Entries) == 0 {
		log.Println("[StartCheck] 対象フィードに記事が見つかりませんでした。")
		return
	}

	latestEntry := result.Entries[0]
	log.Printf("[StartCheck] 最新記事を発見: %s", latestEntry.Title)

	// すでに処理済み（タイトルに [★ がついているなど）でなければ、LLM処理を走らせる
	// ※ただし、今回は新規記事として別フィードに入れるので、元記事のタイトルに [★ はないはずですが安全のため
	processArticleWithLLM(latestEntry)
}

// ==========================================
// 🧠 LLM推論＆Minifluxインサートロジック (Goroutineで実行)
// ==========================================
func processArticleWithLLM(entry MinifluxEntry) {
	log.Printf("Processing item: %s", entry.Title)

	// ★ バッククォート（`）を使用することで、複数行をそのまま記述できます
	systemPrompt := `
	あなたは優秀なニュースキュレーターです。"rkun"という人物のためにニュースを選別して届けます。
	rkunは以下の趣味嗜好を持っています。
	- Webのエンジニアで最先端のWebやクラウドインフラ技術に興味があります。
	- ギークなガジェットにも興味があり、電子工作などDIYにも関心があります。
	- 自動車も好きで、特に古い日本車のスポーツカーやSUVが好みです。最新の電気自動車の話題にも興味があります。彼の愛車はインプレッサWRXです。
	- 日本の政治経済の最新の動きについても関心があります。
	- 暗号通貨、特にステーブルコインに興味があります。暗号通貨の為替については余り関心がありません。
	記事を日本語で要約し、rkunが読む価値があるかについて1から10のスコアを付けてください。
	必ず以下のJSON形式のみで回答してください。他のテキストや説明は一切含めないでください。"

	{
	  "score": 8,
	  "title_ja": "[タイトル]",
	  "summary_ja": "[3行程度の日本語の要約文]",
	  "comment": "[どうしてrkunが読むべき記事なのかの理由]"
	}`

	userContent := fmt.Sprintf("Title: %s\n\nContent: %s", entry.Title, entry.Content)

	llmReq := LLMRequest{
		Model:       LLMModelName,
		Temperature: 0.2,
		Messages: []LLMMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userContent},
		},
	}

	reqBody, _ := json.Marshal(llmReq)

	// 1. ローカルLLMサーバーへポスト
	resp, err := http.Post(LLMServerURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		log.Printf("LLM Server Error: %v", err)
		return
	}
	defer resp.Body.Close()

	var llmResp LLMResponse
	if err := json.NewDecoder(resp.Body).Decode(&llmResp); err != nil {
		log.Printf("Failed to decode LLM response: %v", err)
		return
	}

	// マークダウンの ```json を削るトリミング処理
	rawJSON := llmResp.Choices[0].Message.Content
	rawJSON = strings.TrimPrefix(rawJSON, "```json")
	rawJSON = strings.TrimSuffix(rawJSON, "```")
	rawJSON = strings.TrimSpace(rawJSON)

	var output LLMOutput
	if err := json.Unmarshal([]byte(rawJSON), &output); err != nil {
		log.Printf("Failed to parse LLM Output JSON: %v", err)
		return
	}

	log.Printf("▶ LLM Evaluation - Score: %d / Title: %s", output.Score, output.TitleJa)

	client := &http.Client{Timeout: 10 * time.Second}

	// 2. スコアが閾値以上なら新規インサート
	if output.Score >= ScoreThreshold {
		formattedHTML := fmt.Sprintf(`
			<div style='background-color: #f0f7ff; padding: 15px; border-left: 5px solid #0066cc; margin-bottom: 20px; border: black 1px;'>
				<h3>🤖 AIによる要約 (Score: %d/10)</h3><br>
				<p>%s</p>
				<h3>📝 推薦文</h3><br>
				<p>%s</p>
				<br>
			</div>
				%s
			`,
			output.Score,
			strings.ReplaceAll(output.SummaryJa, "\n", "<br>"),
			strings.ReplaceAll(output.Comment, "\n", "<br>"),
			entry.Content,
		)

		insertPayload := MinifluxInsertPayload{
			Title:       fmt.Sprintf("[★%d] %s", output.Score, output.TitleJa),
			Status:      "unread",
			URL:         entry.URL,
			Author:      "SmartMiniflux",
			PublishedAt: time.Now().Unix(),
			Content:     formattedHTML,
			Tags:        []string{"smart"},
		}

		insertBody, _ := json.Marshal(insertPayload)
		insertURL := fmt.Sprintf("%s/v1/feeds/%d/entries/import", MinifluxURL, AIFeedID)

		req, _ := http.NewRequest("POST", insertURL, bytes.NewBuffer(insertBody))
		req.Header.Set("X-Auth-Token", MinifluxAPIKey)
		req.Header.Set("Content-Type", "application/json")

		insResp, err := client.Do(req)
		if err != nil {
			log.Printf("Failed to insert entry into Miniflux: %v", err)
		} else {
			insResp.Body.Close()
			log.Printf("成功: 新しい翻訳記事をインサートしました。")
		}
		// ★ 2. Discord Webhook への通知処理を追加
		discordPayload := DiscordPayload{
			Embeds: []DiscordEmbed{
				{
					Title:       fmt.Sprintf("[★%d] %s", output.Score, output.TitleJa),
					Description: fmt.Sprintf("**🤖 AI要約**\n%s", output.SummaryJa),
					URL:         entry.URL, // タイトルをクリックしたときに元記事に飛べるリンク
					Color:       3447003,   // 埋め込みの左端の線の色（鮮やかな青色）
					Timestamp:   time.Now().Format(time.RFC3339),
					Fields: []DiscordField{
						{
							Name:   "コメント",
							Value:  output.Comment,
							Inline: false,
						},
						{
							Name:   "元記事タイトル",
							Value:  entry.Title,
							Inline: false,
						},
					},
				},
			},
		}

		discordBody, _ := json.Marshal(discordPayload)
		discResp, err := http.Post(DiscordWebhookURL, "application/json", bytes.NewBuffer(discordBody))
		if err != nil {
			log.Printf("Discord通知に失敗: %v", err)
		} else {
			discResp.Body.Close()
			log.Printf("成功: Discordへの通知を送信しました。")
		}
	}
}

// ==========================================
// 🌐 Webhook 受付ハンドラー
// ==========================================
func handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var webhook MinifluxWebhook
	if err := json.NewDecoder(r.Body).Decode(&webhook); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	log.Printf("body: %+v", webhook)

	if webhook.EventType == "new_entries" {
		for _, entry := range webhook.Entries {
			// 無限ループ防止: スクリプト自身が書き込んだAIフィードの記事はスキップする
			if entry.FeedID == AIFeedID {
				continue
			}

			// ★ Goの本領発揮
			go processArticleWithLLM(entry)
		}
	}

	// Minifluxに即座に応答を返す（タイムアウト防止）
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"status":"accepted"}`))
}

func main() {
	checkLatestEntryOnStart()
	http.HandleFunc("/webhook", handleWebhook)
	log.Println("Starting server on :8000...")
	if err := http.ListenAndServe(":8000", nil); err != nil {
		log.Fatal(err)
	}
}
