package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// ==========================================
// 📤 スコア閾値以上の記事をDiscordへ通知する
// ==========================================
func sendDiscordNotification(entry MinifluxEntry, output LLMOutput) {
	discordPayload := DiscordPayload{
		Embeds: []DiscordEmbed{
			{
				Title:       fmt.Sprintf("[★%d] %s", output.Score, output.TitleJa),
				Description: fmt.Sprintf("**🤖 AI要約**\n%s", output.SummaryJa),
				URL:         entry.URL, // タイトルをクリックしたときに元記事に飛べるリンク
				Color:       3447003,   // 埋め込みの左端の線の色（鮮やかな青色）
				Timestamp:   time.Now().Format(time.RFC3339),
				Fields: []DiscordField{
					{
						Name:   "コメント",
						Value:  output.Comment,
						Inline: false,
					},
					{
						Name:   "元記事タイトル",
						Value:  entry.Title,
						Inline: false,
					},
				},
			},
		},
	}

	discordBody, _ := json.Marshal(discordPayload)
	discResp, err := http.Post(DiscordWebhookURL, "application/json", bytes.NewBuffer(discordBody))
	if err != nil {
		log.Printf("Discord通知に失敗: %v", err)
		return
	}
	discResp.Body.Close()
	log.Printf("成功: Discordへの通知を送信しました。")
}