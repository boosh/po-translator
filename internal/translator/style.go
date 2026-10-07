package translator

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Style is the house style translations follow, read from a YAML file kept
// with the project being translated. Instructions apply to every language,
// such as the brand voice; Languages pins down what only makes sense per
// language, such as the variant, form of address and glossary.
type Style struct {
	Instructions string                   `yaml:"instructions"`
	Languages    map[string]LanguageStyle `yaml:"languages"`
}

// LanguageStyle is the style for one locale code, as it appears in the .po
// file's path (e.g. "es" or "en_US").
type LanguageStyle struct {
	// Name is how the prompt names the language, which is where the variant
	// is set: "Spanish (Spain)" rather than "es".
	Name         string            `yaml:"name"`
	Instructions string            `yaml:"instructions"`
	Glossary     map[string]string `yaml:"glossary"`
}

// Target is the language a chunk is translated into, with the style guidance
// the prompt carries for it.
type Target struct {
	Code         string
	Name         string
	Instructions []string
	Glossary     []GlossaryEntry
}

// GlossaryEntry is one source term and the translation it must always get.
type GlossaryEntry struct {
	Term        string
	Translation string
}

// LoadStyle reads and validates a style file. Unknown keys are rejected so
// that a misspelt setting fails loudly instead of being silently ignored.
func LoadStyle(path string) (*Style, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read style file: %w", err)
	}

	var style Style
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&style); err != nil {
		return nil, fmt.Errorf("failed to parse style file %s: %w", path, err)
	}

	if len(style.Languages) == 0 {
		return nil, fmt.Errorf("style file %s defines no languages", path)
	}
	for code, lang := range style.Languages {
		if strings.TrimSpace(lang.Name) == "" {
			return nil, fmt.Errorf("style file %s: language %q has no name", path, code)
		}
		for term, translation := range lang.Glossary {
			if strings.TrimSpace(term) == "" || strings.TrimSpace(translation) == "" {
				return nil, fmt.Errorf("style file %s: language %q has an empty glossary term or translation", path, code)
			}
		}
	}
	return &style, nil
}

// Target resolves the style for a locale code. Without a style file the code
// is all the prompt gets. With one, a language it doesn't cover is an error:
// translating it anyway would quietly drop the house style.
func (s *Style) Target(code string) (Target, error) {
	if s == nil {
		return Target{Code: code, Name: code}, nil
	}

	lang, ok := s.Languages[code]
	if !ok {
		return Target{}, fmt.Errorf("style file has no entry for language %q", code)
	}

	target := Target{Code: code, Name: strings.TrimSpace(lang.Name)}
	for _, text := range []string{s.Instructions, lang.Instructions} {
		if text = strings.TrimSpace(text); text != "" {
			target.Instructions = append(target.Instructions, text)
		}
	}

	terms := make([]string, 0, len(lang.Glossary))
	for term := range lang.Glossary {
		terms = append(terms, term)
	}
	sort.Strings(terms)
	for _, term := range terms {
		target.Glossary = append(target.Glossary, GlossaryEntry{Term: term, Translation: lang.Glossary[term]})
	}
	return target, nil
}
