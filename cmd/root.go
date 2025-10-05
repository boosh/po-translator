package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/chai2010/gettext-go/po"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"po-translator/internal/logger"
	"po-translator/internal/translator"
)

var (
	logLevel    string
	logFile     string
	provider    string
	model       string
	apiKey      string
	temperature float32
	maxRetries  int
	retryDelay  time.Duration
	chunkSize       int
	dryRun          bool
	strict          bool
	dedupe          bool
	fix             bool
	maxTranslations int
)

var rootCmd = &cobra.Command{
	Use:   "po-translator <glob-pattern...>",
	Short: "A CLI tool to translate .po files using AI.",
	Long:  `A Go CLI tool that manages Django/gettext .po file translations using AI services.`,
	Args:  cobra.MinimumNArgs(1),
	Run:   run,
}

func init() {
	godotenv.Load()

	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().StringVar(&logFile, "log-file", "", "Path to log file for output")
	rootCmd.PersistentFlags().BoolVar(&strict, "strict", false, "Exit immediately on any error")

	rootCmd.Flags().StringVar(&provider, "provider", "", "AI provider: anthropic, google (required)")
	rootCmd.Flags().StringVar(&model, "model", "", "Model name to use for translation (required)")
	rootCmd.Flags().StringVar(&apiKey, "api-key", "", "API key (optional, overrides env vars)")
	rootCmd.Flags().Float32Var(&temperature, "temperature", 0.3, "Temperature for AI generation")
	rootCmd.Flags().IntVar(&maxRetries, "max-retries", 3, "Max retries for failed API calls")
	rootCmd.Flags().DurationVar(&retryDelay, "retry-delay", 2*time.Second, "Delay between retries")
	rootCmd.Flags().IntVar(&chunkSize, "chunk-size", 50, "Number of entries to translate per AI request")
	rootCmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "Process files but do not write any changes")
	rootCmd.Flags().BoolVar(&dedupe, "dedupe", false, "Deduplicate entries with the same msgid and msgstr")
	rootCmd.Flags().BoolVar(&fix, "fix", false, "Fix unescaped percent signs in msgid and msgstr")
	rootCmd.Flags().IntVar(&maxTranslations, "max-translations", 0, "Max number of entries to translate per file (0 for no limit)")

	rootCmd.MarkFlagRequired("provider")
	rootCmd.MarkFlagRequired("model")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) {
	logger.Setup(logLevel, logFile)
	ctx := context.Background()

	providerConfig := translator.Config{
		Provider:    provider,
		Model:       model,
		APIKey:      apiKey,
		Temperature: temperature,
		MaxRetries:  maxRetries,
	}
	aiProvider, err := translator.NewProvider(ctx, providerConfig)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create AI provider")
	}
	log.Info().Str("provider", provider).Str("model", model).Msg("Initialized AI provider")

	start := time.Now()
	var allFiles []string
	for _, pattern := range args {
		matches, err := doublestar.Glob(os.DirFS("."), pattern)
		if err != nil {
			log.Warn().Err(err).Str("pattern", pattern).Msg("Invalid glob pattern")
			continue
		}
		allFiles = append(allFiles, matches...)
	}

	if len(allFiles) == 0 {
		log.Fatal().Msg("No .po files found matching patterns")
	}

	log.Info().Int("count", len(allFiles)).Strs("patterns", args).Msg("Found .po files to process")

	if dryRun {
		log.Info().Msg("DRY RUN ENABLED: No changes will be written to files.")
	}

	var wg sync.WaitGroup
	var totalErrors int64
	var totalTranslations int64

	semaphore := make(chan struct{}, 4)

	for _, file := range allFiles {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			translations, err := processFile(ctx, aiProvider, path, chunkSize)
			if err != nil {
				log.Error().Err(err).Str("file", path).Msg("Failed to process file")
				totalErrors++
				if strict {
					log.Fatal().Msg("Strict mode enabled, exiting on first error.")
				}
			} else {
				if translations > 0 {
					log.Info().Int64("translations", translations).Str("file", path).Msg("Completed file processing")
				} else {
					log.Info().Str("file", path).Msg("Completed file processing (no new translations)")
				}
				totalTranslations += translations
			}
		}(file)
	}

	wg.Wait()

	elapsed := time.Since(start).Seconds()
	summary := log.Info().
		Int("total_files", len(allFiles)).
		Int64("total_translations", totalTranslations).
		Int64("total_errors", totalErrors).
		Float64("elapsed_seconds", elapsed)

	if totalErrors > 0 {
		summary.Msg("Processing completed with errors")
		os.Exit(1)
	} else {
		summary.Msg("All files processed successfully")
	}
}

func processFile(ctx context.Context, provider translator.Provider, path string, chunkSize int) (int64, error) {
	fileLog := log.With().Str("file", path).Logger()
	fileLog.Info().Msg("Processing started")
	start := time.Now()

	poFile, err := po.LoadFile(path)
	if err != nil {
		return 0, fmt.Errorf("failed to load po file: %w", err)
	}

	var madeChanges bool

	if dedupe {
		dedupedCount, dedupedChanges, err := deduplicateEntries(poFile)
		if err != nil {
			return 0, fmt.Errorf("failed to deduplicate entries: %w", err)
		}
		if dedupedChanges {
			fileLog.Info().Int("count", dedupedCount).Msg("Deduplicated entries")
			madeChanges = true
		}
	}

	if fix {
		fixCount, fixChanges := fixUnescapedPercents(poFile)
		if fixChanges {
			fileLog.Info().Int("count", fixCount).Msg("Fixed unescaped percent signs")
			madeChanges = madeChanges || fixChanges
		}
	}

	fuzzyCount, fuzzyChanges := clearFuzzyEntries(poFile)
	if fuzzyChanges {
		fileLog.Info().Int("count", fuzzyCount).Msg("Cleared fuzzy entries")
		madeChanges = madeChanges || fuzzyChanges
	}

	// Sort messages by msgid to ensure stable order
	if sortChanges := sortMessages(poFile); sortChanges {
		fileLog.Info().Msg("Reordered messages by msgid")
		madeChanges = true
	}

	// Step 2: Find untranslated entries
	type job struct {
		Index int
		Msg   po.Message
	}
	var untranslatedJobs []job
	for i, msg := range poFile.Messages {
		if msg.MsgId != "" && msg.MsgStr == "" {
			untranslatedJobs = append(untranslatedJobs, job{Index: i, Msg: msg})
		}
	}

	if maxTranslations > 0 && len(untranslatedJobs) > maxTranslations {
		fileLog.Info().Int("limit", maxTranslations).Int("original_count", len(untranslatedJobs)).Msg("Limiting translations to max-translations")
		untranslatedJobs = untranslatedJobs[:maxTranslations]
	}

	if len(untranslatedJobs) == 0 {
		fileLog.Info().Msg("No untranslated entries found")
		if !dryRun && madeChanges {
			if err := poFile.Save(path); err != nil {
				return 0, fmt.Errorf("failed to save file after clearing fuzzy flags: %w", err)
			}
		}
		return 0, nil
	}

	fileLog.Info().Int("count", len(untranslatedJobs)).Msg("Found untranslated entries")

	if dryRun {
		if madeChanges {
			fileLog.Info().Msg("DRY RUN: Fuzzy entries would be cleared.")
		}
		if len(untranslatedJobs) > 0 {
			fileLog.Info().Msg("DRY RUN: The following entries would be translated:")
			for _, job := range untranslatedJobs {
				fileLog.Info().Str("msgid", job.Msg.MsgId).Msg("  - Would translate")
			}
		}
		return int64(len(untranslatedJobs)), nil
	}

	// Step 3: Translate in chunks and save progressively
	var totalTranslated int64 = 0
	for i := 0; i < len(untranslatedJobs); i += chunkSize {
		end := i + chunkSize
		if end > len(untranslatedJobs) {
			end = len(untranslatedJobs)
		}
		jobChunk := untranslatedJobs[i:end]

		msgChunk := make([]po.Message, len(jobChunk))
		for i, j := range jobChunk {
			msgChunk[i] = j.Msg
		}

		chunkLog := fileLog.With().
			Int("chunk_start", i+1).
			Int("chunk_end", end).
			Int("total", len(untranslatedJobs)).
			Logger()
		chunkLog.Info().Str("provider", provider.String()).Str("model", model).Msg("Translating chunk")

		chunkStart := time.Now()
		translations, err := translator.TranslateChunk(ctx, provider, msgChunk, path)
		if err != nil {
			// Return the count of translations completed so far, even if this chunk failed
			return totalTranslated, fmt.Errorf("translation error in chunk %d-%d: %w", i+1, end, err)
		}
		chunkLog.Debug().Float64("duration_seconds", time.Since(chunkStart).Seconds()).Msg("Chunk translation took")

		if len(translations) == 0 {
			chunkLog.Info().Msg("Chunk processed, but no translations were returned.")
			continue
		}

		for j, translation := range translations {
			originalIndex := jobChunk[j].Index
			poFile.Messages[originalIndex].MsgStr = translation
		}
		totalTranslated += int64(len(translations))
		madeChanges = true

		// Save progress after each chunk
		if !dryRun {
			chunkLog.Debug().Msg("Saving progress to file")
			if err := poFile.Save(path); err != nil {
				// Return the count of translations successfully processed so far, along with the save error
				return totalTranslated, fmt.Errorf("failed to save progress after chunk %d-%d: %w", i+1, end, err)
			}
		}
	}

	fileLog.Info().Float64("duration_seconds", time.Since(start).Seconds()).Msg("Completed processing file")
	return totalTranslated, nil
}

// clearFuzzyEntries iterates through a .po file and clears the fuzzy flag
// and any obsolete comments from messages that are marked as fuzzy.
// It returns the number of fuzzy entries that were cleared and a boolean
// indicating whether any changes were made.
func clearFuzzyEntries(poFile *po.File) (fuzzyCount int, madeChanges bool) {
	for i := range poFile.Messages {
		if poFile.Messages[i].Comment.GetFuzzy() {
			// When a fuzzy entry is re-translated, we clear the fuzzy flag,
			// the previous translation, and the obsolete msgid comments.
			var newFlags []string
			for _, flag := range poFile.Messages[i].Comment.Flags {
				if flag != "fuzzy" {
					newFlags = append(newFlags, flag)
				}
			}
			poFile.Messages[i].Comment.Flags = newFlags
			poFile.Messages[i].Comment.PrevMsgContext = ""
			poFile.Messages[i].Comment.PrevMsgId = ""
			poFile.Messages[i].MsgStr = ""
			fuzzyCount++
			madeChanges = true
		}
	}
	return fuzzyCount, madeChanges
}

// deduplicateEntries removes duplicate messages from a .po file.
// It identifies duplicates based on a composite key of msgctxt and msgid.
// When duplicates are found with the same msgstr, it keeps one entry
// (preferring a non-fuzzy one) and removes the others.
// It will return an error if two entries have the same msgctxt and msgid but
// different msgstr values.
func deduplicateEntries(poFile *po.File) (dedupedCount int, madeChanges bool, err error) {
	// key: "msgctxt|msgid" -> list of indices
	msgidMap := make(map[string][]int)
	for i, msg := range poFile.Messages {
		if msg.MsgId == "" {
			continue // Skip header
		}
		key := fmt.Sprintf("%s|%s", msg.MsgContext, msg.MsgId)
		msgidMap[key] = append(msgidMap[key], i)
	}

	indicesToRemove := make(map[int]struct{})

	for _, indices := range msgidMap {
		if len(indices) <= 1 {
			continue
		}

		// Check for different msgstr values within the group
		firstMsgStr := poFile.Messages[indices[0]].MsgStr
		for i := 1; i < len(indices); i++ {
			currentIndex := indices[i]
			if poFile.Messages[currentIndex].MsgStr != firstMsgStr {
				return 0, false, fmt.Errorf(
					"duplicate msgid '%s' (context: '%s') with different msgstr: '%s' vs '%s'",
					poFile.Messages[indices[0]].MsgId,
					poFile.Messages[indices[0]].MsgContext,
					firstMsgStr,
					poFile.Messages[currentIndex].MsgStr,
				)
			}
		}

		// All msgstr are the same; decide which entry to keep.
		// We prefer to keep a non-fuzzy entry.
		keepIndex := -1
		for _, index := range indices {
			if !poFile.Messages[index].Comment.GetFuzzy() {
				keepIndex = index
				break
			}
		}

		// If all entries are fuzzy, keep the first one. Its fuzzy state is preserved.
		if keepIndex == -1 {
			keepIndex = indices[0]
		}

		// Mark all other entries in the group for removal.
		for _, index := range indices {
			if index != keepIndex {
				indicesToRemove[index] = struct{}{}
			}
		}
	}

	dedupedCount = len(indicesToRemove)
	madeChanges = dedupedCount > 0

	if !madeChanges {
		return 0, false, nil
	}

	// Now, remove the marked entries by building a new slice.
	if dedupedCount > 0 {
		var newMessages []po.Message
		for i, msg := range poFile.Messages {
			if _, shouldRemove := indicesToRemove[i]; !shouldRemove {
				newMessages = append(newMessages, msg)
			}
		}
		poFile.Messages = newMessages
	}

	return dedupedCount, madeChanges, nil
}

// fixUnescapedPercents escapes standalone '%' characters in msgid and msgstr
// fields of a .po file. It is designed to fix issues where strings like "10%"
// are not correctly escaped as "10%%". It avoids escaping valid Python/C-style
// format specifiers like `%(name)s` or `%d`.
func fixUnescapedPercents(poFile *po.File) (fixCount int, madeChanges bool) {
	// Regex to match all known valid format specifiers, or a lone percent sign.
	// The order is important: match longer specific patterns first.
	re := regexp.MustCompile(`%%|%\([^\)]*\)[sdifouxXeEgGcp]|%[sdifouxXeEgGcp]|%`)

	fixer := func(s string) (string, bool) {
		stringChanged := false
		replacer := func(match string) string {
			if match == "%" {
				stringChanged = true
				return "%%"
			}
			// It was a valid specifier or an already-escaped percent, so return it unchanged.
			return match
		}
		result := re.ReplaceAllStringFunc(s, replacer)
		return result, stringChanged
	}

	totalChanges := false
	count := 0

	for i := range poFile.Messages {
		msgChanged := false

		if poFile.Messages[i].MsgId != "" {
			fixedMsgId, idChanged := fixer(poFile.Messages[i].MsgId)
			if idChanged {
				poFile.Messages[i].MsgId = fixedMsgId
				msgChanged = true
			}
		}

		if poFile.Messages[i].MsgStr != "" {
			fixedMsgStr, strChanged := fixer(poFile.Messages[i].MsgStr)
			if strChanged {
				poFile.Messages[i].MsgStr = fixedMsgStr
				msgChanged = true
			}
		}

		if msgChanged {
			totalChanges = true
			count++
		}
	}
	return count, totalChanges
}

// sortMessages sorts the messages in a .po file by msgid.
// It preserves the header entry at the beginning of the file.
// sortMessages sorts the messages in a .po file by msgid.
// It preserves the header entry at the beginning of the file.
// It returns a boolean indicating whether the order of messages was changed.
func sortMessages(poFile *po.File) bool {
	if len(poFile.Messages) <= 1 {
		return false
	}

	// Capture the original order of msgids to detect changes.
	originalOrder := make([][]byte, 0, len(poFile.Messages))
	for _, msg := range poFile.Messages {
		originalOrder = append(originalOrder, []byte(msg.MsgId))
	}

	// Separate the header (first message, usually with empty msgid)
	header := poFile.Messages[0]
	messages := poFile.Messages[1:]

	sort.SliceStable(messages, func(i, j int) bool {
		return messages[i].MsgId < messages[j].MsgId
	})

	// Re-assemble the messages with the header at the top
	poFile.Messages = append([]po.Message{header}, messages...)

	// Check if the order has actually changed
	for i, msg := range poFile.Messages {
		if !bytes.Equal(originalOrder[i], []byte(msg.MsgId)) {
			return true // Order has changed
		}
	}

	return false // Order is the same
}