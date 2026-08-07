package main

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/go-co-op/gocron/v2"
	"golang.org/x/net/html"
)

// DigestCron にしたがってダイジェストを送信するスケジューラー
func startDigestScheduler() error {
	s, err := gocron.NewScheduler()
	if err != nil {
		return fmt.Errorf("スケジューラーの作成に失敗: %w", err)
	}

	// DigestCron で設定された間隔でダイジェスト送信ジョブを登録
	var j gocron.Job
	j, err = s.NewJob(
		gocron.CronJob(DigestCron, false),
		gocron.NewTask(func() {
			// ダイジェストを送信する（DigestCron で設定された間隔で実行される）
			since, err := j.LastRunCompletedAt()
			if err != nil || since.IsZero() {
				since = time.Now().Add(12 * time.Hour)
			}

			log.Printf("[Digest] %s以降の記事を取得する", since.Format(time.DateTime))

			entries, err := getFeedEntries(since)
			if err != nil {
				log.Printf("記事の取得に失敗: %v", err)
				return
			}

			summary, err := generateDailySummary(entries)
			if err != nil {
				log.Printf("サマリの作成に失敗: %v", err)
				return
			}

			if err := sendDiscordNotification(DiscordPayload{
				Embeds: []DiscordEmbed{
					{
						Title:       time.Now().Format(time.DateOnly) + " のダイジェスト",
						Description: summary,
					},
				},
			}); err != nil {
				log.Printf("Discordへの送信に失敗: %v", err)
			}
		}),
	)
	if err != nil {
		return fmt.Errorf("ダイジェストジョブの登録に失敗: %w", err)
	}

	s.Start()
	log.Printf("[Digest] ダイジェストスケジューラーを開始しました (cron: %s)", DigestCron)
	return nil
}

// 直近1日(24時間)に AI_FEED_ID へ追加された記事を取得し、ニュース番組風にまとめてDiscordへ送信する
func generateDailySummary(entries []MinifluxEntry) (string, error) {

	var filtered []MinifluxEntry
	for _, e := range entries {
		// スコアが閾値以上の記事のみを対象とする
		if scoreFromTitle(e.Title) >= int(DigestScoreThreshold) {
			filtered = append(filtered, e)
		}
	}

	if len(filtered) == 0 {
		return "", fmt.Errorf("[Digest] 直近24時間にダイジェスト対象の記事がありません。")
	}

	log.Printf("[Digest] 直近24時間に %d 件の記事をダイジェスト対象として送信します。", len(filtered))

	// LLMを使ってニュース番組風の文面を生成する
	description, err := generateDigestDescription(filtered)
	if err != nil {
		return "", fmt.Errorf("[Digest] ダイジェスト文面の生成に失敗しました: %v", err)
	}

	return fmt.Sprintf("直近24時間に追加された記事をニュース番組形式でお届けします。\n\n%s", description), nil
}

// MinifluxエントリのTitle冒頭の "[★N]" からスコアを抽出する（パース不能なら0）
func scoreFromTitle(title string) int {
	start := strings.Index(title, "[★")
	if start < 0 {
		return 0
	}
	rest := title[start+len("[★"):]
	end := strings.IndexAny(rest, " ]")
	if end < 0 {
		return 0
	}
	score, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}
	return score
}

// AIフィード記事のContentから冒頭の要約(summary)と推薦コメント(comment)を抽出する
func extractSummaryComment(content string) (summary, comment string) {
	z := html.NewTokenizer(strings.NewReader(content))
	var pending string // "summary" or "comment"（次の<p>タグに適用する）
	var mode string
	var buf []string

	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return summary, comment
		case html.TextToken:
			txt := string(z.Text())
			if pending == "" && mode == "" {
				// <h3>の見出しテキストからモードを判定
				if strings.Contains(txt, "要約") {
					pending = "summary"
				} else if strings.Contains(txt, "推薦") {
					pending = "comment"
				}
			}
			if mode != "" {
				buf = append(buf, txt)
			}
		case html.StartTagToken:
			tn, _ := z.TagName()
			switch string(tn) {
			case "p":
				if pending != "" {
					mode = pending
					pending = ""
					buf = buf[:0]
				}
			case "br":
				// <p>内の改行表現
				if mode != "" {
					buf = append(buf, "\n")
				}
			}
		case html.EndTagToken:
			tn, _ := z.TagName()
			if string(tn) == "p" && mode != "" {
				text := strings.TrimSpace(strings.Join(buf, ""))
				if mode == "summary" {
					summary = text
				} else if mode == "comment" {
					comment = text
				}
				mode = ""
			}
		}
	}
}
