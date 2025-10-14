package cmd

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/chai2010/gettext-go/po"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"po-translator/internal/git"
	"po-translator/internal/logger"
	"po-translator/internal/translator"
)

var newProvider = translator.NewProvider

var (
	logLevel          string
	logFile           string
	provider          string
	model             string
	apiKey            string
	temperature       float32
	maxRetries        int
	retryDelay        time.Duration
	chunkSize         int
	dryRun            bool
	strict            bool
	dedupe            bool
	fix               bool
	maxTranslations   int
	noTranslate       bool
	revertIfUnchanged bool
	yes               bool
	logPrompt         bool
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
	rootCmd.Flags().BoolVar(&noTranslate, "no-translate", false, "Disable translation and only perform other operations (e.g., --fix, --dedupe)")
	rootCmd.Flags().BoolVar(&revertIfUnchanged, "revert-if-unchanged", false, "Revert .po file to its git HEAD version if no new translations were made")
	rootCmd.Flags().BoolVarP(&yes, "yes", "y", false, "Automatically answer yes to all prompts and skip confirmation")
	rootCmd.Flags().BoolVar(&logPrompt, "log-prompt", false, "Log the full prompt sent to the AI provider (for debugging)")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) {
	logger.Setup(logLevel, logFile)
	ctx := context.Background()
	start := time.Now()

	if dryRun {
		log.Info().Msg("DRY RUN ENABLED: No changes will be written to files.")
	}

	allFiles, err := findFiles(args)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to find files")
	}

	// --- Pass 1: Pre-process and clean up files ---
	log.Info().Msg("--- Starting pre-processing pass (dedupe, fix, sort) ---")
	var filesToTranslate []string
	var totalErrors int64
	untranslatedFileMessages := make(map[string][]po.Message)

	for _, path := range allFiles {
		untranslated, err := preprocessFile(path)
		if err != nil {
			log.Error().Err(err).Str("file", path).Msg("Failed during pre-processing")
			atomic.AddInt64(&totalErrors, 1)
			if strict {
				log.Fatal().Msg("Strict mode enabled, exiting on first error.")
			}
			continue
		}
		if len(untranslated) > 0 {
			filesToTranslate = append(filesToTranslate, path)
			untranslatedFileMessages[path] = untranslated
		}
	}

	if totalErrors > 0 {
		logSummary(len(allFiles), 0, totalErrors, start)
		return
	}

	// --- Confirmation Step ---
	if noTranslate || len(filesToTranslate) == 0 {
		log.Info().Msg("Pre-processing complete. No new translations needed.")
		logSummary(len(allFiles), 0, totalErrors, start)
		return
	}

	totalUntranslated := 0
	for _, msgs := range untranslatedFileMessages {
		totalUntranslated += len(msgs)
	}

	if !yes && !dryRun {
		fmt.Println("The following files have untranslated entries:")
		for _, path := range filesToTranslate {
			messages := untranslatedFileMessages[path]
			fmt.Printf("  - %s: %d entries\n", path, len(messages))
			for _, msg := range messages {
				// To avoid spamming the console, truncate long msgids
				msgidForDisplay := msg.MsgId
				if len(msgidForDisplay) > 70 {
					msgidForDisplay = msgidForDisplay[:67] + "..."
				}
				// Escape newlines to keep the output clean
				msgidForDisplay = strings.ReplaceAll(msgidForDisplay, "\n", "\\n")
				fmt.Printf("    - msgid: \"%s\"\n", msgidForDisplay)
			}
		}
		fmt.Printf("\nTotal: %d untranslated entries across %d file(s).\n", totalUntranslated, len(filesToTranslate))
		fmt.Print("Proceed with translation? (y/N): ")

		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		if !strings.EqualFold(strings.TrimSpace(input), "y") && !strings.EqualFold(strings.TrimSpace(input), "yes") {
			fmt.Println("Translation cancelled.")
			logSummary(len(allFiles), 0, totalErrors, start)
			return
		}
	}

	// --- Pass 2: Translate files that need it ---
	log.Info().Msg("--- Starting translation pass ---")
	aiProvider, err := initProvider(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize AI provider")
	}

	var wg sync.WaitGroup
	var totalTranslations int64
	semaphore := make(chan struct{}, 4)

	for _, path := range filesToTranslate {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			translations, err := translateFile(ctx, aiProvider, p, chunkSize)
			if err != nil {
				log.Error().Err(err).Str("file", p).Msg("Failed to translate file")
				atomic.AddInt64(&totalErrors, 1)
			} else {
				atomic.AddInt64(&totalTranslations, translations)
			}
		}(path)
	}
	wg.Wait()

	logSummary(len(allFiles), totalTranslations, totalErrors, start)
}

func findFiles(patterns []string) ([]string, error) {
	var allFiles []string
	for _, pattern := range patterns {
		matches, err := doublestar.Glob(os.DirFS("."), pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid glob pattern: %s", pattern)
		}
		allFiles = append(allFiles, matches...)
	}
	if len(allFiles) == 0 {
		return nil, fmt.Errorf("no .po files found matching patterns")
	}
	return allFiles, nil
}

func initProvider(ctx context.Context) (translator.Provider, error) {
	if provider == "" {
		return nil, fmt.Errorf("error: --provider is required unless --no-translate is set")
	}
	if model == "" {
		return nil, fmt.Errorf("error: --model is required unless --no-translate is set")
	}

	providerConfig := translator.Config{
		Provider:    provider,
		Model:       model,
		APIKey:      apiKey,
		Temperature: temperature,
		MaxRetries:  maxRetries,
		LogPrompt:   logPrompt,
	}
	p, err := newProvider(ctx, providerConfig)
	if err != nil {
		return nil, err
	}
	log.Info().Str("provider", provider).Str("model", model).Msg("Initialized AI provider")
	return p, nil
}

func preprocessFile(path string) ([]po.Message, error) {
	fileLog := log.With().Str("file", path).Logger()
	fileLog.Info().Msg("Pre-processing file")

	poFile, err := po.LoadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load po file: %w", err)
	}

	var madeChanges bool
	if fix {
		count, changed := fixUnescapedPercents(poFile)
		if changed {
			fileLog.Info().Int("count", count).Msg("Fixed unescaped percent signs")
			madeChanges = true
		}
	}
	if dedupe {
		count, changed, err := deduplicateEntries(poFile)
		if err != nil {
			return nil, err
		}
		if changed {
			fileLog.Info().Int("count", count).Msg("Deduplicated entries")
			madeChanges = true
		}
	}
	if count, changed := clearFuzzyEntries(poFile); changed {
		fileLog.Info().Int("count", count).Msg("Cleared fuzzy entries")
		madeChanges = true
	}
	if changed := sortMessages(poFile); changed {
		fileLog.Info().Msg("Reordered messages by msgid")
		madeChanges = true
	}

	if madeChanges && !dryRun {
		if err := savePoFile(poFile, path); err != nil {
			return nil, fmt.Errorf("failed to save file after pre-processing: %w", err)
		}
		reloadedPoFile, err := po.LoadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to reload .po file after saving: %w", err)
		}
		poFile = reloadedPoFile
	}

	var untranslated []po.Message
	for _, msg := range poFile.Messages {
		if !isMessageTranslated(msg) {
			untranslated = append(untranslated, msg)
		}
	}

	if len(untranslated) == 0 && revertIfUnchanged {
		err := git.RevertFile(path)
		if err == nil {
			fileLog.Info().Msg("Reverted file to git HEAD version to avoid spurious commit")
			return nil, nil
		}
		fileLog.Warn().Err(err).Msg("Could not revert file to git HEAD, saving cleaned-up version instead")
	}

	return untranslated, nil
}

func isMessageTranslated(msg po.Message) bool {
	if msg.MsgId == "" {
		return true // Skip empty msgids
	}
	if msg.MsgIdPlural == "" {
		return msg.MsgStr != "" // Simple case: no plural
	}
	if len(msg.MsgStrPlural) == 0 {
		return false // Plural form exists but no translations
	}
	for _, s := range msg.MsgStrPlural {
		if s == "" {
			return false
		}
	}
	return true
}

func getNPlurals(header po.Header) int {
	if header.PluralForms == "" {
		return 2 // Default for many languages
	}
	parts := strings.Split(header.PluralForms, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "nplurals=") {
			valStr := strings.TrimPrefix(part, "nplurals=")
			n, err := strconv.Atoi(valStr)
			if err == nil && n > 0 {
				return n
			}
		}
	}
	return 2 // Default if parsing fails
}

func translateFile(ctx context.Context, provider translator.Provider, path string, chunkSize int) (int64, error) {
	fileLog := log.With().Str("file", path).Logger()
	fileLog.Info().Msg("Translating file")

	poFile, err := po.LoadFile(path)
	if err != nil {
		return 0, fmt.Errorf("failed to load po file for translation: %w", err)
	}

	type job struct{ Index int; Msg po.Message }
	var untranslatedJobs []job
	for i, msg := range poFile.Messages {
		if !isMessageTranslated(msg) {
			untranslatedJobs = append(untranslatedJobs, job{Index: i, Msg: msg})
		}
	}

	if len(untranslatedJobs) == 0 {
		return 0, nil
	}

	if maxTranslations > 0 && len(untranslatedJobs) > maxTranslations {
		fileLog.Info().Int("limit", maxTranslations).Int("original_count", len(untranslatedJobs)).Msg("Limiting translations to max-translations")
		untranslatedJobs = untranslatedJobs[:maxTranslations]
	}

	if dryRun {
		fileLog.Info().Int("count", len(untranslatedJobs)).Msg("DRY RUN: Would translate entries")
		return int64(len(untranslatedJobs)), nil
	}

	var totalTranslated int64
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

		nplurals := getNPlurals(poFile.MimeHeader)
		translations, err := translator.TranslateChunk(ctx, provider, msgChunk, path, nplurals)
		if err != nil {
			return totalTranslated, fmt.Errorf("translation error in chunk %d-%d: %w", i+1, end, err)
		}

		for j, translation := range translations {
			originalIndex := jobChunk[j].Index
			poFile.Messages[originalIndex].MsgStr = translation.MsgStr
			if len(translation.PluralStr) > 0 {
				poFile.Messages[originalIndex].MsgStrPlural = translation.PluralStr
			}
		}
		totalTranslated += int64(len(translations))

		if err := savePoFile(poFile, path); err != nil {
			return totalTranslated, fmt.Errorf("failed to save progress after chunk %d-%d: %w", i+1, end, err)
		}
	}
	return totalTranslated, nil
}

func logSummary(fileCount int, translationCount, errorCount int64, start time.Time) {
	elapsed := time.Since(start).Seconds()
	summary := log.Info().
		Int("total_files", fileCount).
		Int64("total_translations", translationCount).
		Int64("total_errors", errorCount).
		Float64("elapsed_seconds", elapsed)

	if errorCount > 0 {
		summary.Msg("Processing completed with errors")
		os.Exit(1)
	} else {
		summary.Msg("All files processed successfully")
	}
}

func clearFuzzyEntries(poFile *po.File) (fuzzyCount int, madeChanges bool) {
	for i := range poFile.Messages {
		if !poFile.Messages[i].Comment.GetFuzzy() || poFile.Messages[i].Comment.PrevMsgId == "" {
			continue
		}

		currentMsgId := strings.TrimSpace(poFile.Messages[i].MsgId)
		prevMsgId := strings.TrimSpace(poFile.Messages[i].Comment.PrevMsgId)

		if currentMsgId != prevMsgId {
			poFile.Messages[i].MsgStr = ""
		}

		var newFlags []string
		for _, flag := range poFile.Messages[i].Comment.Flags {
			if flag != "fuzzy" {
				newFlags = append(newFlags, flag)
			}
		}
		poFile.Messages[i].Comment.Flags = newFlags
		poFile.Messages[i].Comment.PrevMsgContext = ""
		poFile.Messages[i].Comment.PrevMsgId = ""

		fuzzyCount++
		madeChanges = true
	}
	return fuzzyCount, madeChanges
}

func deduplicateEntries(poFile *po.File) (dedupedCount int, madeChanges bool, err error) {
	msgidMap := make(map[string][]int)
	for i, msg := range poFile.Messages {
		if msg.MsgId == "" {
			continue
		}
		key := fmt.Sprintf("%s|%s", msg.MsgContext, msg.MsgId)
		msgidMap[key] = append(msgidMap[key], i)
	}

	indicesToRemove := make(map[int]struct{})
	for _, indices := range msgidMap {
		if len(indices) <= 1 {
			continue
		}

		// Check for conflicting translations among duplicates
		firstMsgStr := ""
		hasTranslation := false
		for _, index := range indices {
			msgStr := poFile.Messages[index].MsgStr
			if msgStr != "" {
				if hasTranslation && msgStr != firstMsgStr {
					return 0, false, fmt.Errorf("duplicate msgid '%s' (context: '%s') with conflicting msgstr: '%s' vs '%s'",
						poFile.Messages[indices[0]].MsgId, poFile.Messages[indices[0]].MsgContext, firstMsgStr, msgStr)
				}
				firstMsgStr = msgStr
				hasTranslation = true
			}
		}

		// Determine which entry to keep
		keepIndex := -1
		// Prefer non-fuzzy entries
		for _, index := range indices {
			if !poFile.Messages[index].Comment.GetFuzzy() {
				keepIndex = index
				break
			}
		}
		// Otherwise, just keep the first one
		if keepIndex == -1 {
			keepIndex = indices[0]
		}

		// Merge comments and mark others for removal
		for _, index := range indices {
			if index != keepIndex {
				mergeComments(&poFile.Messages[keepIndex].Comment, &poFile.Messages[index].Comment)
				indicesToRemove[index] = struct{}{}
			}
		}
		poFile.Messages[keepIndex].MsgStr = firstMsgStr
	}

	if len(indicesToRemove) > 0 {
		madeChanges = true
		var newMessages []po.Message
		for i, msg := range poFile.Messages {
			if _, shouldRemove := indicesToRemove[i]; !shouldRemove {
				newMessages = append(newMessages, msg)
			}
		}
		poFile.Messages = newMessages
		return len(indicesToRemove), true, nil
	}

	return 0, false, nil
}

func mergeComments(target *po.Comment, source *po.Comment) {
	if source.TranslatorComment != "" {
		if target.TranslatorComment == "" {
			target.TranslatorComment = source.TranslatorComment
		} else if !strings.Contains(target.TranslatorComment, source.TranslatorComment) {
			target.TranslatorComment += "\n" + source.TranslatorComment
		}
	}
	if source.ExtractedComment != "" {
		if target.ExtractedComment == "" {
			target.ExtractedComment = source.ExtractedComment
		} else if !strings.Contains(target.ExtractedComment, source.ExtractedComment) {
			target.ExtractedComment += "\n" + source.ExtractedComment
		}
	}
	target.ReferenceFile = appendIfMissing(target.ReferenceFile, source.ReferenceFile...)

	// Only merge flags that are not "fuzzy"
	for _, flag := range source.Flags {
		if flag != "fuzzy" {
			target.Flags = appendIfMissing(target.Flags, flag)
		}
	}
}

func appendIfMissing(slice []string, items ...string) []string {
	for _, item := range items {
		found := false
		for _, s := range slice {
			if s == item {
				found = true
				break
			}
		}
		if !found {
			slice = append(slice, item)
		}
	}
	return slice
}

func fixUnescapedPercents(poFile *po.File) (fixCount int, madeChanges bool) {
	re := regexp.MustCompile(`%%|%\([^\)]*\)[sdifouxXeEgGcp]|%[sdifouxXeEgGcp]|%`)
	fixer := func(s string) (string, bool) {
		stringChanged := false
		replacer := func(match string) string {
			if match == "%" {
				stringChanged = true
				return "%%"
			}
			return match
		}
		result := re.ReplaceAllStringFunc(s, replacer)
		return result, stringChanged
	}
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
			madeChanges = true
			count++
		}
	}
	return count, madeChanges
}

func sortMessages(poFile *po.File) bool {
	if len(poFile.Messages) <= 1 {
		return false
	}
	originalOrder := make([][]byte, 0, len(poFile.Messages))
	for _, msg := range poFile.Messages {
		originalOrder = append(originalOrder, []byte(msg.MsgId))
	}
	sort.SliceStable(poFile.Messages, func(i, j int) bool {
		return poFile.Messages[i].MsgId < poFile.Messages[j].MsgId
	})
	for i, msg := range poFile.Messages {
		if !bytes.Equal(originalOrder[i], []byte(msg.MsgId)) {
			return true
		}
	}
	return false
}

func savePoFile(poFile *po.File, path string) error {
	var buf bytes.Buffer
	buf.WriteString(poFile.MimeHeader.String())
	buf.WriteString("\n")

	for _, msg := range poFile.Messages {
		buf.WriteString(msg.String())
		buf.WriteString("\n")
	}

	return os.WriteFile(path, buf.Bytes(), 0644)
}
