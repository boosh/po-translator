package translator

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
)

// providerSpec describes one supported AI provider: the environment variables
// that hold its credentials, the endpoint it speaks to, and how to build it.
// No spec carries a model name — models come and go too quickly to pin one in
// the binary, so the model is always supplied through configuration.
type providerSpec struct {
	Name string
	// KeyEnvVars are checked in order; the first non-empty one wins.
	KeyEnvVars []string
	// DefaultBaseURL is empty for providers that do not take one.
	DefaultBaseURL string
	New            func(ctx context.Context, config Config) (Provider, error)
}

// providerSpecs is the single list of supported providers. The provider used
// for a run is whichever one has credentials in the environment.
var providerSpecs = []providerSpec{
	{
		Name:           "openrouter",
		KeyEnvVars:     []string{"OPENROUTER_API_KEY"},
		DefaultBaseURL: "https://openrouter.ai/api/v1",
		New:            NewOpenAICompatibleProvider,
	},
	{
		Name:           "digitalocean",
		KeyEnvVars:     []string{"DIGITALOCEAN_MODEL_ACCESS_KEY"},
		DefaultBaseURL: "https://inference.do-ai.run/v1",
		New:            NewOpenAICompatibleProvider,
	},
	{
		Name:       "google",
		KeyEnvVars: []string{"GOOGLE_API_KEY", "GEMINI_API_KEY"},
		New:        NewGoogleProvider,
	},
}

// ResolveProvider picks the provider to use and the key to authenticate with.
// With a name it selects that provider; without one it infers the provider from
// the environment, which works as long as only one provider has a key set.
// Several keys can be present when the environment is shared with other tools,
// so that case asks for a name rather than guessing. A non-empty apiKey
// overrides whatever the environment holds for the chosen provider.
func ResolveProvider(name, apiKey string) (providerSpec, string, error) {
	if name != "" {
		spec, ok := findProviderSpec(name)
		if !ok {
			return providerSpec{}, "", fmt.Errorf("unknown provider %q: choose one of %s", name, strings.Join(ProviderNames(), ", "))
		}
		if apiKey == "" {
			apiKey = providerKey(spec)
		}
		if apiKey == "" {
			return providerSpec{}, "", fmt.Errorf(
				"no API key found for provider %s: set %s, or pass --api-key",
				spec.Name, strings.Join(spec.KeyEnvVars, " or "),
			)
		}
		return spec, apiKey, nil
	}

	var matched []providerSpec
	var envKey string
	var setVars []string

	for _, spec := range providerSpecs {
		key := providerKey(spec)
		if key == "" {
			continue
		}
		matched = append(matched, spec)
		envKey = key
		setVars = append(setVars, providerKeyVar(spec))
	}

	switch len(matched) {
	case 0:
		if apiKey != "" {
			return providerSpec{}, "", fmt.Errorf(
				"an API key was given but no provider to use it with: name one of %s with --provider or LLM_PROVIDER",
				strings.Join(ProviderNames(), ", "),
			)
		}
		return providerSpec{}, "", fmt.Errorf("no API key found: set one of %s", strings.Join(allKeyEnvVars(), ", "))
	case 1:
		if apiKey == "" {
			apiKey = envKey
		}
		return matched[0], apiKey, nil
	default:
		sort.Strings(setVars)
		return providerSpec{}, "", fmt.Errorf(
			"credentials for more than one provider found (%s): use --provider or LLM_PROVIDER to choose one of %s",
			strings.Join(setVars, ", "), strings.Join(matchedNames(matched), ", "),
		)
	}
}

// findProviderSpec looks up a provider by name, case-insensitively.
func findProviderSpec(name string) (providerSpec, bool) {
	for _, spec := range providerSpecs {
		if strings.EqualFold(spec.Name, name) {
			return spec, true
		}
	}
	return providerSpec{}, false
}

// providerKey returns the provider's API key from the environment, taking the
// first of its variables that holds a value.
func providerKey(spec providerSpec) string {
	for _, envVar := range spec.KeyEnvVars {
		if value := os.Getenv(envVar); value != "" {
			return value
		}
	}
	return ""
}

// providerKeyVar returns the name of the variable providerKey took its value
// from, for use in messages.
func providerKeyVar(spec providerSpec) string {
	for _, envVar := range spec.KeyEnvVars {
		if os.Getenv(envVar) != "" {
			return envVar
		}
	}
	return ""
}

// matchedNames lists the names of the given specs.
func matchedNames(specs []providerSpec) []string {
	var names []string
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	return names
}

// ProviderNames lists every supported provider, for help text and messages.
func ProviderNames() []string {
	return matchedNames(providerSpecs)
}

// allKeyEnvVars lists every credential variable the tool recognises.
func allKeyEnvVars() []string {
	var vars []string
	for _, spec := range providerSpecs {
		vars = append(vars, spec.KeyEnvVars...)
	}
	return vars
}

// NewProvider builds the AI provider for this run, named by config.Provider or
// inferred from the credentials in the environment when that is empty.
func NewProvider(ctx context.Context, config Config) (Provider, error) {
	spec, apiKey, err := ResolveProvider(config.Provider, config.APIKey)
	if err != nil {
		return nil, err
	}

	if config.Model == "" {
		return nil, fmt.Errorf("no model set: use --model or the LLM_MODEL env var")
	}

	config.Provider = spec.Name
	config.APIKey = apiKey
	if config.BaseURL == "" {
		config.BaseURL = spec.DefaultBaseURL
	}

	return spec.New(ctx, config)
}
