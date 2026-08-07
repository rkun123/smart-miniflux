package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// ==========================================
// 🧠 LLM推論＆記事処理コーディネーター
// ==========================================
func processArticleWithLLM(entry MinifluxEntry) {
	log.Printf("Processing item: %s", entry.Title)

	output, err := evaluateWithLLM(entry)
	if err != nil {
		log.Printf("LLM処理がすべてのリトライに失敗しました: %v", err)
		return
	}

	log.Printf("▶ LLM Evaluation - Score: %d / Title: %s", output.Score, output.TitleJa)

	// スコアが閾値以上なら新規インサート + Discord通知
	if output.Score >= ScoreThreshold {
		insertToMiniflux(entry, output)
		sendDiscordNotification(entry, output)
	}
}

// ==========================================
// 🤖 ローカルLLMに記事を評価させる（リトライ込み）
// ==========================================
func evaluateWithLLM(entry MinifluxEntry) (LLMOutput, error) {
	// システムプロンプトは環境変数 SYSTEM_PROMPT から取得（未設定時はデフォルト値）
	// JSONスキーマの指示はコード側で自動的に末尾に追加する
	systemPrompt := SystemPrompt + `

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

	return output, lastErr
}