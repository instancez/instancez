// Package sendgrid implements domain.EmailSender using the SendGrid API.
package sendgrid

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/instancez/instancez/internal/domain"
)

const apiURL = "https://api.sendgrid.com/v3/mail/send"

// Sender implements domain.EmailSender using SendGrid.
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

type sgMail struct {
	Personalizations []sgPersonalization `json:"personalizations"`
	From             sgEmail             `json:"from"`
	Subject          string              `json:"subject"`
	Content          []sgContent         `json:"content"`
}

type sgPersonalization struct {
	To []sgEmail `json:"to"`
}

type sgEmail struct {
	Email string `json:"email"`
}

type sgContent struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

func (s *Sender) Send(ctx context.Context, msg domain.EmailMessage) error {
	to := make([]sgEmail, len(msg.To))
	for i, addr := range msg.To {
		to[i] = sgEmail{Email: addr}
	}

	content := []sgContent{}
	if msg.Text != "" {
		content = append(content, sgContent{Type: "text/plain", Value: msg.Text})
	}
	if msg.HTML != "" {
		content = append(content, sgContent{Type: "text/html", Value: msg.HTML})
	}

	body := sgMail{
		Personalizations: []sgPersonalization{{To: to}},
		From:             sgEmail{Email: msg.From},
		Subject:          msg.Subject,
		Content:          content,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("sendgrid: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("sendgrid: new request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("sendgrid: send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sendgrid: API error %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
