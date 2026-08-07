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

func getFeedEntries(since time.Time) ([]MinifluxEntry, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	url := fmt.Sprintf("%s/v1/feeds/%d/entries?direction=desc&limit=200&published_after=%d",
		MinifluxURL, AIFeedID, since.Unix())

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("X-Auth-Token", MinifluxAPIKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("[Digest] Miniflux APIへの接続に失敗: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Entries []MinifluxEntry `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("[Digest] レスポンスのパースに失敗: %v", err)
	}

	return result.Entries, nil
}
