package translator

import (
	"context"
	"fmt"

	"github.com/chai2010/gettext-go/po"
)

// AnthropicProvider is a stub implementation for Anthropic's Claude models.
// It is currently disabled to prevent compilation errors.
type AnthropicProvider struct{}

// NewAnthropicProvider returns an error, as this provider is disabled.
func NewAnthropicProvider(config Config) (*AnthropicProvider, error) {
	return nil, fmt.Errorf("the Anthropic provider is temporarily disabled")
}

// String returns the name of the provider.
func (p *AnthropicProvider) String() string {
	return "anthropic"
}

// Translate returns an error, as this provider is disabled.
func (p *AnthropicProvider) Translate(ctx context.Context, messages []po.Message, sourceLang, targetLang string, nplurals int) ([]TranslationResult, error) {
	return nil, fmt.Errorf("the Anthropic provider is temporarily disabled")
}