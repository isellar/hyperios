package llm

import (
	"context"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const Model = anthropic.ModelClaudeSonnet4_6

// Client wraps the Anthropic SDK for simple prompt→JSON calls.
type Client struct {
	inner *anthropic.Client
}

// NewClient creates an Anthropic client. API key is read from ANTHROPIC_API_KEY
// env var by the SDK automatically; pass an explicit key to override.
func NewClient(apiKey string) *Client {
	opts := []option.RequestOption{}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	c := anthropic.NewClient(opts...)
	return &Client{inner: &c}
}

// Complete sends a system + user prompt and returns the raw text response.
func (c *Client) Complete(ctx context.Context, system, user string) (string, error) {
	msg, err := c.inner.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     Model,
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{
			{Text: system},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(user)),
		},
	})
	if err != nil {
		return "", fmt.Errorf("llm call failed: %w", err)
	}
	if len(msg.Content) == 0 {
		return "", fmt.Errorf("empty response from model")
	}
	var sb strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	return sb.String(), nil
}
