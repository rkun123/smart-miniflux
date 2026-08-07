package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

func sendDiscordNotification(payload DiscordPayload) error {
	discordBody, _ := json.Marshal(payload)
	discResp, err := http.Post(DiscordWebhookURL, "application/json", bytes.NewBuffer(discordBody))
	if err != nil {
		return fmt.Errorf("Discord通知に失敗: %v", err)
	}
	discResp.Body.Close()
	log.Printf("成功: Discordへの通知を送信しました。")
	return nil
}
