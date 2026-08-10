package notify

import (
	"context"
	"fmt"

	"github.com/slack-go/slack"
)

// SlackNotifier sends notifications to a Slack webhook using slack-go/slack.
type SlackNotifier struct {
	webhookURL string
}

// NewSlackNotifier creates a new Slack notifier.
func NewSlackNotifier(webhookURL string) *SlackNotifier {
	return &SlackNotifier{webhookURL: webhookURL}
}

// Notify sends a message to the configured Slack webhook.
func (n *SlackNotifier) Notify(ctx context.Context, message string) error {
	if n.webhookURL == "" {
		return fmt.Errorf("slack webhook URL not configured")
	}

	err := slack.PostWebhook(n.webhookURL, &slack.WebhookMessage{
		Text: message,
	})
	if err != nil {
		return fmt.Errorf("failed to send Slack notification: %w", err)
	}

	return nil
}
