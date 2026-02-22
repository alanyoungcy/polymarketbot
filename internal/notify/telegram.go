package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TelegramSender delivers notifications via the Telegram Bot API.
type TelegramSender struct {
	token  string
	chatID string
	client *http.Client
}

// NewTelegramSender creates a TelegramSender for the given bot token and chat
// ID. It uses a default HTTP client with a 10-second timeout.
func NewTelegramSender(token, chatID string) *TelegramSender {
	return &TelegramSender{
		token:  token,
		chatID: chatID,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Send posts a message to the configured Telegram chat using the sendMessage
// API. The title is rendered in bold using MarkdownV2 syntax.
func (t *TelegramSender) Send(ctx context.Context, title, message string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.token)

	text := fmt.Sprintf("*%s*\n%s", title, message)

	payload := map[string]string{
		"chat_id":    t.chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("telegram: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("telegram: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// Name returns the sender identifier.
func (t *TelegramSender) Name() string {
	return "telegram"
}
