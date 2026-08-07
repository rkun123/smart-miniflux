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
