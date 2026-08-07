package main

// LLMリクエストをシリアル化するためのチャネル
var entryChan = make(chan MinifluxEntry, 100)

// ==========================================
// 🧵 LLMリクエストをシリアルに処理するワーカー
// ==========================================
func entryWorker() {
	for entry := range entryChan {
		processArticleWithLLM(entry)
	}
}