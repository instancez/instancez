package domain

import "context"

// EmailSender is the port for sending emails.
type EmailSender interface {
	Send(ctx context.Context, msg EmailMessage) error
}

// EmailMessage holds the data for a single email.
type EmailMessage struct {
	To      []string
	From    string
	ReplyTo string
	Subject string
	HTML    string
	Text    string
}
