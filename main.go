package main

import (
	"log"
	"net/http"
)

func main() {
	// LLMリクエストをシリアルに処理するワーカーを起動
	go entryWorker()

	// ダイジェスト送信スケジューラーを起動
	if err := startDigestScheduler(); err != nil {
		log.Printf("ダイジェストスケジューラーの起動に失敗: %v", err)
	}

	http.HandleFunc("/webhook", handleWebhook)
	log.Println("Starting server on :8000...")
	if err := http.ListenAndServe(":8000", nil); err != nil {
		log.Fatal(err)
	}
}
