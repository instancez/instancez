// Package resend implements domain.EmailSender using the Resend API.
package resend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/saedx1/ultrabase/internal/domain"
)

const apiURL = "https://api.resend.com/emails"

// Sender implements domain.EmailSender using Resend.
type Sender struct {
	apiKey string
	client *http.Client
}

func New(apiKey string) *Sender {
	return &Sender{
		apiKey: apiKey,
		client: &http.Client{},
	}
}

type sendRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html,omitempty"`
	Text    string   `json:"text,omitempty"`
	ReplyTo string   `json:"reply_to,omitempty"`
}

func (s *Sender) Send(ctx context.Context, msg domain.EmailMessage) error {
	body := sendRequest{
		From:    msg.From,
		To:      msg.To,
		Subject: msg.Subject,
		HTML:    msg.HTML,
		Text:    msg.Text,
		ReplyTo: msg.ReplyTo,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("resend: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("resend: new request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("resend: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("resend: API error %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
