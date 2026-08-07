package main

import (
	"log"
	"net/http"
)

func main() {
	// LLMリクエストをシリアルに処理するワーカーを起動
	go entryWorker()

	checkLatestEntryOnStart()
	http.HandleFunc("/webhook", handleWebhook)
	log.Println("Starting server on :8000...")
	if err := http.ListenAndServe(":8000", nil); err != nil {
		log.Fatal(err)
	}
}