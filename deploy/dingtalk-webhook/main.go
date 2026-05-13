package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type alertManagerWebhook struct {
	Status      string            `json:"status"`
	Alerts      []alert           `json:"alerts"`
	CommonLabels map[string]string `json:"commonLabels"`
}

type alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     string            `json:"startsAt"`
	EndsAt       string            `json:"endsAt"`
}

type dingTalkMessage struct {
	MsgType  string           `json:"msgtype"`
	Markdown dingTalkMarkdown `json:"markdown"`
}

type dingTalkMarkdown struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

type config struct {
	ListenAddr string
	DingURL    string
	Secret     string
}

func loadConfig() config {
	cfg := config{
		ListenAddr: ":8060",
		DingURL:    os.Getenv("DINGTALK_URL"),
		Secret:     os.Getenv("DINGTALK_SECRET"),
	}
	if cfg.DingURL == "" {
		log.Fatal("DINGTALK_URL environment variable is required")
	}
	return cfg
}

func sign(secret string) (string, string) {
	timestamp := fmt.Sprintf("%d", time.Now().UnixMilli())
	stringToSign := timestamp + "\n" + secret
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stringToSign))
	signStr := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return timestamp, url.QueryEscape(signStr)
}

func formatAlert(data alertManagerWebhook) (string, string) {
	var titleParts []string
	if data.Status == "firing" {
		titleParts = append(titleParts, fmt.Sprintf("[FIRING:%d]", len(data.Alerts)))
	} else {
		titleParts = append(titleParts, "[RESOLVED]")
	}
	if v, ok := data.CommonLabels["alertname"]; ok {
		titleParts = append(titleParts, v)
	}
	title := strings.Join(titleParts, " ")

	var b strings.Builder
	for i, alert := range data.Alerts {
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		if s, ok := alert.Annotations["summary"]; ok {
			b.WriteString(fmt.Sprintf("**%s**\n\n", s))
		}
		if d, ok := alert.Annotations["description"]; ok {
			b.WriteString(fmt.Sprintf("%s\n\n", d))
		}
		if alert.StartsAt != "" {
			b.WriteString(fmt.Sprintf("Start: %s\n\n", alert.StartsAt))
		}
		b.WriteString("**Labels:**\n")
		for k, v := range alert.Labels {
			b.WriteString(fmt.Sprintf("- %s: %s\n", k, v))
		}
	}
	return title, b.String()
}

func handleWebhook(cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		var data alertManagerWebhook
		if err := json.Unmarshal(body, &data); err != nil {
			http.Error(w, "Failed to parse alert", http.StatusBadRequest)
			return
		}

		title, text := formatAlert(data)
		msg := dingTalkMessage{
			MsgType: "markdown",
			Markdown: dingTalkMarkdown{
				Title: title,
				Text:  text,
			},
		}

		msgBytes, _ := json.Marshal(msg)

		timestamp, encodedSign := sign(cfg.Secret)
		apiURL := cfg.DingURL
		if strings.Contains(apiURL, "?") {
			apiURL += "&timestamp=" + timestamp + "&sign=" + encodedSign
		} else {
			apiURL += "?timestamp=" + timestamp + "&sign=" + encodedSign
		}

		resp, err := http.Post(apiURL, "application/json; charset=utf-8", bytes.NewReader(msgBytes))
		if err != nil {
			log.Printf("Failed to send to DingTalk: %v", err)
			http.Error(w, "Failed to send to DingTalk", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("DingTalk response: %s %s", resp.Status, string(respBody))

		if resp.StatusCode != http.StatusOK {
			http.Error(w, fmt.Sprintf("DingTalk returned %d", resp.StatusCode), http.StatusBadGateway)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}
}

func main() {
	cfg := loadConfig()
	http.HandleFunc("/dingtalk/ops/send", handleWebhook(cfg))
	log.Printf("Listening on %s", cfg.ListenAddr)
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, nil))
}
