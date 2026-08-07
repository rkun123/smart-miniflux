package main

import (
	"encoding/json"
	"log"
	"net/http"
)

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