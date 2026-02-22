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

// DiscordSender delivers notifications via a Discord webhook.
type DiscordSender struct {
	webhookURL string
	client     *http.Client
}

// NewDiscordSender creates a DiscordSender for the given webhook URL. It uses a
// default HTTP client with a 10-second timeout.
func NewDiscordSender(webhookURL string) *DiscordSender {
	return &DiscordSender{
		webhookURL: webhookURL,
		client:     &http.Client{Timeout: 10 * time.Second},
	}
}

// Send posts a message to the Discord webhook. The title is rendered in bold
// using Discord markdown syntax.
func (d *DiscordSender) Send(ctx context.Context, title, message string) error {
	content := fmt.Sprintf("**%s**\n%s", title, message)

	payload := map[string]string{
		"content": content,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("discord: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("discord: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("discord: send request: %w", err)
	}
	defer resp.Body.Close()

	// Discord returns 204 No Content on success.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("discord: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// Name returns the sender identifier.
func (d *DiscordSender) Name() string {
	return "discord"
}
