package translator

import "context"

// Provider is the interface that all AI providers must implement.
type Provider interface {
	Translate(ctx context.Context, texts []string, sourceLang, targetLang string) ([]string, error)
	String() string
}

// Config holds the configuration for the AI provider.
type Config struct {
	Provider    string
	Model       string
	APIKey      string
	Temperature float32
	MaxRetries  int
}