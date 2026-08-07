package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

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
// 📥 スコア閾値以上の記事をAIフィードへインサートする
// ==========================================
func insertToMiniflux(entry MinifluxEntry, output LLMOutput) {
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

	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("POST", insertURL, bytes.NewBuffer(insertBody))
	req.Header.Set("X-Auth-Token", MinifluxAPIKey)
	req.Header.Set("Content-Type", "application/json")

	insResp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to insert entry into Miniflux: %v", err)
		return
	}
	insResp.Body.Close()
	log.Printf("成功: 新しい翻訳記事をインサートしました。")
}