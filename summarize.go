package main

import "log"

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
	}
}
