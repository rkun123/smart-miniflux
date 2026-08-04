package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
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

	// 定期ダイジェストの実行時刻（HH:MM、24時間表記）
	DigestTime = getEnv("DIGEST_TIME", "01:42")

	// ダイジェストに含める記事の最低スコア
	DigestScoreThreshold = 10

	// LLMリクエストをシリアル化するためのチャネル
	entryChan = make(chan MinifluxEntry, 100)
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
	ID          int64  `json:"id"`
	FeedID      int64  `json:"feed_id"`
	Title       string `json:"title"`
	Content     string `json:"content"`
	URL         string `json:"url"`
	PublishedAt string `json:"published_at"`
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
// 🧵 LLMリクエストをシリアルに処理するワーカー
// ==========================================
func entryWorker() {
	for entry := range entryChan {
		processArticleWithLLM(entry)
	}
}

// ==========================================
// 🧠 LLM推論＆Minifluxインサートロジック
// ==========================================
func processArticleWithLLM(entry MinifluxEntry) {
	log.Printf("Processing item: %s", entry.Title)

	// システムプロンプトは環境変数 SYSTEM_PROMPT から取得（未設定時はデフォルト値）
	// JSONスキーマの指示はコード側で自動的に末尾に追加する
	systemPrompt := SystemPrompt + `

記事を日本語で要約し、rkunが読む価値があるかについて1から10のスコアを付けてください。
必ず以下のJSON形式のみで回答してください。他のテキストや説明は一切含めないでください。"

{
  "score": 8,
  "title_ja": "[タイトル]",
  "summary_ja": "[5行程度の日本語の要約文]",
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

	var output LLMOutput
	var lastErr error

	// リトライループ: LLMリクエスト〜JSONパースまでをリトライ
	for i := 0; i <= LLMRetryCount; i++ {
		if i > 0 {
			log.Printf("LLMリクエスト リトライ %d/%d", i, LLMRetryCount)
			time.Sleep(1 * time.Second)
		}

		// 1. ローカルLLMサーバーへポスト
		req, _ := http.NewRequest("POST", LLMServerURL, bytes.NewBuffer(reqBody))
		req.Header.Set("Content-Type", "application/json")
		if LLMServerToken != "" {
			req.Header.Set("Authorization", "Bearer "+LLMServerToken)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("LLM Server Error: %w", err)
			log.Printf("LLM Server Error (attempt %d/%d): %v", i, LLMRetryCount, err)
			continue
		}

		bodyBytes, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("Failed to read LLM response body: %w", readErr)
			log.Printf("Failed to read LLM response body (attempt %d/%d): %v", i, LLMRetryCount, readErr)
			continue
		}

		var llmResp LLMResponse
		if decodeErr := json.Unmarshal(bodyBytes, &llmResp); decodeErr != nil {
			lastErr = fmt.Errorf("Failed to decode LLM response: %w", decodeErr)
			log.Printf("Failed to decode LLM response (attempt %d/%d): %v", i, LLMRetryCount, decodeErr)
			log.Printf("LLM raw response (attempt %d/%d): %s", i, LLMRetryCount, string(bodyBytes))
			continue
		}

		// マークダウンの ```json を削るトリミング処理
		rawJSON := llmResp.Choices[0].Message.Content
		rawJSON = strings.TrimPrefix(rawJSON, "```json")
		rawJSON = strings.TrimSuffix(rawJSON, "```")
		rawJSON = strings.TrimSpace(rawJSON)

		if err := json.Unmarshal([]byte(rawJSON), &output); err != nil {
			lastErr = fmt.Errorf("Failed to parse LLM Output JSON: %w", err)
			log.Printf("Failed to parse LLM Output JSON (attempt %d/%d): %v", i, LLMRetryCount, err)
			log.Printf("LLM raw response (attempt %d/%d): %s", i, LLMRetryCount, string(bodyBytes))
			continue
		}

		// 成功
		lastErr = nil
		break
	}

	if lastErr != nil {
		log.Printf("LLM処理がすべてのリトライに失敗しました: %v", lastErr)
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
	}
}

// ==========================================
// 📅 毎日の定期ダイジェスト（Discordのみに送信）
// ==========================================

// DigestTime(HH:MM) に毎日ダイジェストを送信するスケジューラー
func startDigestScheduler() {
	parts := strings.SplitN(DigestTime, ":", 2)
	if len(parts) != 2 {
		log.Printf("[Digest] DIGEST_TIME の形式が不正です（HH:MM 期待、実際: %s）。ダイジェストを無効にします。", DigestTime)
		return
	}
	hour, errH := strconv.Atoi(parts[0])
	min, errM := strconv.Atoi(parts[1])
	if errH != nil || errM != nil || hour < 0 || hour > 23 || min < 0 || min > 59 {
		log.Printf("[Digest] DIGEST_TIME の時刻が不正です（HH:MM 期待、実際: %s）。ダイジェストを無効にします。", DigestTime)
		return
	}

	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), hour, min, 0, 0, now.Location())
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		log.Printf("[Digest] 次の定期ダイジェストを %s に実行予定（%s）", next.Format("2006-01-02 15:04"), DigestTime)
		time.Sleep(time.Until(next))

		sendDailyDigest()
	}
}

// 直近1日(24時間)に AI_FEED_ID へ追加された記事を取得し、ニュース番組風にまとめてDiscordへ送信する
func sendDailyDigest() {
	if DiscordWebhookURL == "" {
		log.Printf("[Digest] DISCORD_WEBHOOK_URL が未設定のため、ダイジェストを送信しません。")
		return
	}

	since := time.Now().Add(-24 * time.Hour)
	client := &http.Client{Timeout: 10 * time.Second}

	url := fmt.Sprintf("%s/v1/feeds/%d/entries?direction=desc&limit=200", MinifluxURL, AIFeedID)

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("X-Auth-Token", MinifluxAPIKey)

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[Digest] Miniflux APIへの接続に失敗: %v", err)
		return
	}
	defer resp.Body.Close()

	// 直近24時間に追加された、AIフィードの記事のみを抽出
	var result struct {
		Entries []MinifluxEntry `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("[Digest] レスポンスのパースに失敗: %v", err)
		return
	}

	var items []MinifluxEntry
	for _, e := range result.Entries {
		// 直近24時間に追加され、かつスコアが閾値以上の記事のみを対象とする
		t, err := time.Parse(time.RFC3339, e.PublishedAt)
		if err != nil {
			continue
		}
		if t.After(since) && scoreFromTitle(e.Title) >= DigestScoreThreshold {
			items = append(items, e)
		}
	}

	if len(items) == 0 {
		log.Println("[Digest] 直近24時間にダイジェスト対象の記事がありません。")
		return
	}

	log.Printf("[Digest] 直近24時間に %d 件の記事をダイジェスト対象として送信します。", len(items))

	// LLMを使ってニュース番組風の文面を生成する
	description, err := generateDigestDescription(items)
	if err != nil {
		log.Printf("[Digest] ダイジェスト文面の生成に失敗しました: %v", err)
		return
	}

	digestPayload := DiscordPayload{
		Embeds: []DiscordEmbed{
			{
				Title:       fmt.Sprintf("📺 今日のダイジェスト（%s）", time.Now().Format("1月2日")),
				Description: "直近24時間に追加された記事をニュース番組形式でお届けします。\n\n" + description,
				Color:       3447003,
				Timestamp:   time.Now().Format(time.RFC3339),
			},
		},
	}

	body, _ := json.Marshal(digestPayload)
	discResp, err := http.Post(DiscordWebhookURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		log.Printf("[Digest] Discord通知に失敗: %v", err)
		return
	}
	discResp.Body.Close()
	log.Printf("[Digest] 成功: ダイジェストをDiscordへ送信しました（%d件）。", len(items))
}

// MinifluxエントリのTitle冒頭の "[★N]" からスコアを抽出する（パース不能なら0）
func scoreFromTitle(title string) int {
	start := strings.Index(title, "[★")
	if start < 0 {
		return 0
	}
	rest := title[start+len("[★"):]
	end := strings.IndexAny(rest, " ]")
	if end < 0 {
		return 0
	}
	score, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}
	return score
}

// AIフィード記事のContentから冒頭の要約(summary)と推薦コメント(comment)を抽出する
func extractSummaryComment(content string) (summary, comment string) {
	z := html.NewTokenizer(strings.NewReader(content))
	var pending string // "summary" or "comment"（次の<p>タグに適用する）
	var mode string
	var buf []string

	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return summary, comment
		case html.TextToken:
			txt := string(z.Text())
			if pending == "" && mode == "" {
				// <h3>の見出しテキストからモードを判定
				if strings.Contains(txt, "要約") {
					pending = "summary"
				} else if strings.Contains(txt, "推薦") {
					pending = "comment"
				}
			}
			if mode != "" {
				buf = append(buf, txt)
			}
		case html.StartTagToken:
			tn, _ := z.TagName()
			switch string(tn) {
			case "p":
				if pending != "" {
					mode = pending
					pending = ""
					buf = buf[:0]
				}
			case "br":
				// <p>内の改行表現
				if mode != "" {
					buf = append(buf, "\n")
				}
			}
		case html.EndTagToken:
			tn, _ := z.TagName()
			if string(tn) == "p" && mode != "" {
				text := strings.TrimSpace(strings.Join(buf, ""))
				if mode == "summary" {
					summary = text
				} else if mode == "comment" {
					comment = text
				}
				mode = ""
			}
		}
	}
}

// LLMを使って直近の記事をニュース番組風の文面にまとめる
func generateDigestDescription(entries []MinifluxEntry) (string, error) {
	var list []string
	for i, e := range entries {
		// 各記事は冒頭の要約と推薦コメントのみをLLMに渡す
		summary, comment := extractSummaryComment(e.Content)
		list = append(list, fmt.Sprintf("%d\nタイトル: %s\n要約: %s\nコメント: %s", i+1, e.Title, summary, comment))
	}
	articleList := strings.Join(list, "\n\n")

	systemPrompt := `あなたは優秀なニュースキャスターです。今日の重要ニュースをニュース番組のように伝える原稿を作成してください。
以下の指示に必ず従ってください。
- 各ニュースについて一段落で、記事のタイトルと、その記事がなぜ重要なのかを要約して伝える原稿を作成する。
- ニュース番組のアナウンサーのような、親しみやすく簡潔な日本語で作成する。
- マークダウンや記号を使わず、プレーンテキストのみで出力する。
- 全体の長さは4000文字以内に収める。`

	userContent := fmt.Sprintf("今日の番組で取り上げる記事のリストです。\n\n%s\n\nこれらをニュース番組風にまとめてください。", articleList)

	llmReq := LLMRequest{
		Model:       LLMModelName,
		Temperature: 0.7,
		Messages: []LLMMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userContent},
		},
	}
	reqBody, _ := json.Marshal(llmReq)

	req, _ := http.NewRequest("POST", LLMServerURL, bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	if LLMServerToken != "" {
		req.Header.Set("Authorization", "Bearer "+LLMServerToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("LLM Server Error: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", fmt.Errorf("Failed to read LLM response body: %w", readErr)
	}

	var llmResp LLMResponse
	if decodeErr := json.Unmarshal(bodyBytes, &llmResp); decodeErr != nil {
		return "", fmt.Errorf("Failed to decode LLM response: %w", decodeErr)
	}
	if len(llmResp.Choices) == 0 {
		return "", fmt.Errorf("LLM response has no choices")
	}

	text := strings.TrimSpace(llmResp.Choices[0].Message.Content)
	// 出力長の安全策として1000文字で切り詰める
	const maxLen = 4000
	if len(text) > maxLen {
		text = text[:maxLen]
	}
	return text, nil
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

	// Minifluxに即座に応答を返す（タイムアウト防止）
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"status":"accepted"}`))

	if webhook.EventType == "new_entries" {
		for _, entry := range webhook.Entries {
			// 無限ループ防止: スクリプト自身が書き込んだAIフィードの記事はスキップする
			if entry.FeedID == AIFeedID {
				continue
			}

			// チャネルに送信してシリアル処理（entryWorkerが逐次処理する）
			entryChan <- entry
		}
	}
}

func main() {
	// LLMリクエストをシリアルに処理するワーカーを起動
	go entryWorker()

	// 毎日の定期ダイジェストのスケジューラーを起動
	go startDigestScheduler()

	sendDailyDigest()

	checkLatestEntryOnStart()
	http.HandleFunc("/webhook", handleWebhook)
	log.Println("Starting server on :8000...")
	if err := http.ListenAndServe(":8000", nil); err != nil {
		log.Fatal(err)
	}
}
