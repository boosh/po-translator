package cmd

import (
	"bufio"
	"bytes"
	"context"
	"errors"
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
	"github.com/rs/zerolog"
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
	baseURL           string
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

	rootCmd.Flags().StringVar(&provider, "provider", "", "AI provider to use: "+strings.Join(translator.ProviderNames(), ", ")+" (overrides LLM_PROVIDER; only needed when several providers have credentials set)")
	rootCmd.Flags().StringVar(&model, "model", "", "Model name, or OpenRouter preset, to use for translation (overrides LLM_MODEL; one of the two is required)")
	rootCmd.Flags().StringVar(&apiKey, "api-key", "", "API key for the provider (optional, overrides the provider's env var)")
	rootCmd.Flags().StringVar(&baseURL, "base-url", "", "Base URL for the provider API (optional, overrides LLM_BASE_URL)")
	rootCmd.Flags().Float32Var(&temperature, "temperature", 0.3, "Temperature for AI generation")
	rootCmd.Flags().IntVar(&maxRetries, "max-retries", 3, "Max attempts per chunk, covering both failed API calls and unusable responses")
	rootCmd.Flags().DurationVar(&retryDelay, "retry-delay", 2*time.Second, "Base delay before retrying a failed API call, doubling each attempt")
	rootCmd.Flags().IntVar(&chunkSize, "chunk-size", 50, "Number of entries to translate per AI request")
	rootCmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "Process files but do not write any changes")
	rootCmd.Flags().BoolVar(&dedupe, "dedupe", false, "Deduplicate entries with the same msgid and msgstr")
	rootCmd.Flags().BoolVar(&fix, "fix", false, "Make percent signs in msgstr follow the msgid's escaping, including in new translations")
	rootCmd.Flags().IntVar(&maxTranslations, "max-translations", 0, "Max number of entries to translate across all files (0 for no limit)")
	rootCmd.Flags().BoolVar(&noTranslate, "no-translate", false, "Disable translation and only perform other operations (e.g., --fix, --dedupe)")
	rootCmd.Flags().BoolVar(&revertIfUnchanged, "revert-if-unchanged", false, "Revert .po file to its git HEAD version if its entries match HEAD")
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

	// The provider is set up before the prompt, so that a missing key or model
	// is reported before the work is approved rather than after, and so the
	// summary can name what the translation will run on.
	aiProvider, err := initProvider(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize AI provider")
	}

	totalUntranslated := 0
	for _, msgs := range untranslatedFileMessages {
		totalUntranslated += len(msgs)
	}

	fileLimits := allocateTranslationBudget(filesToTranslate, untranslatedFileMessages, maxTranslations)

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
		if maxTranslations > 0 && maxTranslations < totalUntranslated {
			fmt.Printf("Only the first %d will be translated (--max-translations).\n", maxTranslations)
		}
		fmt.Printf("Translating with %s, model %s.\n", aiProvider.String(), model)
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

	var wg sync.WaitGroup
	var totalTranslations int64
	semaphore := make(chan struct{}, 4)

	for _, path := range filesToTranslate {
		limit, ok := fileLimits[path]
		if !ok {
			continue
		}
		wg.Add(1)
		go func(p string, limit int) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			translations, err := translateFile(ctx, aiProvider, p, chunkSize, limit)
			if err != nil {
				log.Error().Err(err).Str("file", p).Msg("Failed to translate file")
				atomic.AddInt64(&totalErrors, 1)
			} else {
				atomic.AddInt64(&totalTranslations, translations)
			}
		}(path, limit)
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

// initProvider builds the AI provider. Its flags all fall back to environment
// variables; leaving the provider unset lets translator.NewProvider infer it
// from whichever API key is present.
func initProvider(ctx context.Context) (translator.Provider, error) {
	if provider == "" {
		provider = os.Getenv("LLM_PROVIDER")
	}
	if model == "" {
		model = os.Getenv("LLM_MODEL")
	}
	if baseURL == "" {
		baseURL = os.Getenv("LLM_BASE_URL")
	}

	providerConfig := translator.Config{
		Provider:    provider,
		Model:       model,
		APIKey:      apiKey,
		BaseURL:     baseURL,
		Temperature: temperature,
		MaxRetries:  maxRetries,
		RetryDelay:  retryDelay,
		LogPrompt:   logPrompt,
	}
	p, err := newProvider(ctx, providerConfig)
	if err != nil {
		return nil, err
	}
	log.Info().Str("provider", p.String()).Str("model", model).Msg("Initialized AI provider")
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
	if count, changed := clearInvalidTranslations(poFile, &fileLog); changed {
		fileLog.Info().Int("count", count).Msg("Cleared translations whose placeholders don't match their msgid")
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

	nplurals := getNPlurals(poFile.MimeHeader)
	var untranslated []po.Message
	for _, msg := range poFile.Messages {
		if !isMessageTranslated(msg, nplurals) {
			untranslated = append(untranslated, msg)
		}
	}

	if revertIfUnchanged && !dryRun {
		// Reverting is only meant to drop header churn such as a new
		// POT-Creation-Date. Entries that differ from HEAD (removed strings,
		// kept fuzzy translations, uncommitted edits) would be lost by it.
		// Entries still untranslated in HEAD don't stop it: the reverted file
		// has the same entries, so the translation pass works on it unchanged.
		unchanged, err := entriesMatchHead(poFile, path)
		switch {
		case err != nil:
			fileLog.Warn().Err(err).Msg("Could not compare file with git HEAD, saving cleaned-up version instead")
		case !unchanged:
			fileLog.Info().Msg("Entries differ from git HEAD, keeping file")
		default:
			if err := git.RevertFile(path); err != nil {
				fileLog.Warn().Err(err).Msg("Could not revert file to git HEAD, saving cleaned-up version instead")
			} else {
				fileLog.Info().Msg("Reverted file to git HEAD version to avoid spurious commit")
			}
		}
	}

	return untranslated, nil
}

// entriesMatchHead reports whether poFile has the same entries as path's git
// HEAD version, ignoring the header, entry order and comments, none of which
// change what is displayed.
func entriesMatchHead(poFile *po.File, path string) (bool, error) {
	head, err := git.HeadContent(path)
	if err != nil {
		return false, err
	}
	headFile, err := po.Load(head)
	if err != nil {
		return false, fmt.Errorf("failed to parse git HEAD version: %w", err)
	}

	current, committed := renderEntries(poFile), renderEntries(headFile)
	if len(current) != len(committed) {
		return false, nil
	}
	for i := range current {
		if current[i] != committed[i] {
			return false, nil
		}
	}
	return true, nil
}

// renderEntries renders every entry with only its flags kept from its
// comments, sorted, so two files can be compared regardless of formatting and
// order.
func renderEntries(poFile *po.File) []string {
	rendered := make([]string, 0, len(poFile.Messages))
	for _, msg := range poFile.Messages {
		msg.Comment = po.Comment{Flags: msg.Comment.Flags}
		rendered = append(rendered, msg.String())
	}
	sort.Strings(rendered)
	return rendered
}

func isMessageTranslated(msg po.Message, nplurals int) bool {
	if msg.MsgId == "" {
		return true // Skip empty msgids
	}
	if msg.MsgIdPlural == "" {
		return msg.MsgStr != "" // Simple case: no plural
	}
	if len(msg.MsgStrPlural) < nplurals {
		return false // Fewer forms than the language needs
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

// translationJob pairs an untranslated message with its position in the file.
type translationJob struct {
	Index int
	Msg   po.Message
}

// maxChunkSplits caps how far a failing chunk is halved. Models drop entries
// more often on long chunks, so retrying a failed 50 as two 25s often works,
// but splitting all the way down to single entries costs far more requests
// than the last few stragglers are worth.
const maxChunkSplits = 2

// allocateTranslationBudget shares a run-wide cap on translations out between
// files, in the order given, before any are translated concurrently. Files the
// budget doesn't reach are left out of the result. With no cap (budget <= 0)
// every file is included with a limit of 0, meaning unlimited.
func allocateTranslationBudget(paths []string, untranslated map[string][]po.Message, budget int) map[string]int {
	limits := make(map[string]int, len(paths))
	remaining := budget
	for _, path := range paths {
		if budget <= 0 {
			limits[path] = 0
			continue
		}
		if remaining == 0 {
			break
		}
		share := min(len(untranslated[path]), remaining)
		limits[path] = share
		remaining -= share
	}
	return limits
}

// translateFile translates up to limit untranslated entries in one file (0 for
// no limit), saving after every chunk.
func translateFile(ctx context.Context, provider translator.Provider, path string, chunkSize, limit int) (int64, error) {
	fileLog := log.With().Str("file", path).Logger()
	fileLog.Info().Msg("Translating file")

	poFile, err := po.LoadFile(path)
	if err != nil {
		return 0, fmt.Errorf("failed to load po file for translation: %w", err)
	}

	nplurals := getNPlurals(poFile.MimeHeader)
	var untranslatedJobs []translationJob
	for i, msg := range poFile.Messages {
		if !isMessageTranslated(msg, nplurals) {
			untranslatedJobs = append(untranslatedJobs, translationJob{Index: i, Msg: msg})
		}
	}

	if len(untranslatedJobs) == 0 {
		return 0, nil
	}

	if limit > 0 && len(untranslatedJobs) > limit {
		fileLog.Info().Int("limit", limit).Int("original_count", len(untranslatedJobs)).Msg("Limiting translations to this file's share of max-translations")
		untranslatedJobs = untranslatedJobs[:limit]
	}

	if dryRun {
		fileLog.Info().Int("count", len(untranslatedJobs)).Msg("DRY RUN: Would translate entries")
		return int64(len(untranslatedJobs)), nil
	}

	var totalTranslated int64
	var failedChunks, totalChunks int

	for i := 0; i < len(untranslatedJobs); i += chunkSize {
		end := i + chunkSize
		if end > len(untranslatedJobs) {
			end = len(untranslatedJobs)
		}
		totalChunks++

		translated, err := translateJobs(ctx, provider, poFile, untranslatedJobs[i:end], path, nplurals, 0, &fileLog)
		totalTranslated += translated

		// Whatever the chunk did manage is worth keeping, so save before
		// deciding what to do about the error.
		if translated > 0 {
			if saveErr := savePoFile(poFile, path); saveErr != nil {
				return totalTranslated, fmt.Errorf("failed to save progress after chunk %d-%d: %w", i+1, end, saveErr)
			}
		}

		if err != nil {
			// One bad chunk should not cost the file its remaining chunks; the
			// entries it missed are still untranslated, so a later run picks
			// them up on their own.
			failedChunks++
			fileLog.Error().Err(err).Int("chunk_start", i+1).Int("chunk_end", end).Msg("Chunk failed, moving on to the next one")
			if strict {
				return totalTranslated, fmt.Errorf("translation error in chunk %d-%d: %w", i+1, end, err)
			}
		}
	}

	if failedChunks > 0 {
		return totalTranslated, fmt.Errorf("%d of %d chunks failed", failedChunks, totalChunks)
	}
	return totalTranslated, nil
}

// translateJobs translates one chunk and writes the results into poFile. When
// the whole chunk comes back unusable it is halved and the halves are tried
// separately, up to maxChunkSplits deep. The smaller size applies only to this
// chunk; the rest of the file continues at the configured chunk size.
func translateJobs(ctx context.Context, provider translator.Provider, poFile *po.File, jobs []translationJob, path string, nplurals, depth int, fileLog *zerolog.Logger) (int64, error) {
	msgChunk := make([]po.Message, len(jobs))
	for i, j := range jobs {
		msgChunk[i] = j.Msg
	}

	translations, err := translator.TranslateChunk(ctx, provider, msgChunk, path, nplurals)
	if err == nil {
		var accepted int64
		for j, translation := range translations {
			candidate := jobs[j].Msg
			if candidate.MsgIdPlural == "" {
				candidate.MsgStr = translation.MsgStr
			} else {
				candidate.MsgStr = ""
				candidate.MsgStrPlural = translation.PluralStr
			}
			if fix {
				fixMessagePercents(&candidate)
			}

			// A bad entry is dropped rather than failing the chunk: it stays
			// untranslated, so the next run asks for it again.
			if !isMessageTranslated(candidate, nplurals) {
				fileLog.Warn().Str("msgid", candidate.MsgId).Msg("Discarding translation with missing or empty forms")
				continue
			}
			if err := checkTranslation(candidate, nplurals); err != nil {
				fileLog.Warn().Err(err).Str("msgid", candidate.MsgId).Msg("Discarding translation with mismatched placeholders")
				continue
			}

			originalIndex := jobs[j].Index
			poFile.Messages[originalIndex].MsgStr = candidate.MsgStr
			poFile.Messages[originalIndex].MsgStrPlural = candidate.MsgStrPlural
			accepted++
		}
		return accepted, nil
	}

	// Only a bad answer is worth asking again for in smaller pieces. A refused
	// key or an unreachable endpoint fails the same way however little is sent.
	if !errors.Is(err, translator.ErrUnusableResponse) || depth >= maxChunkSplits || len(jobs) < 2 {
		return 0, err
	}

	half := len(jobs) / 2
	fileLog.Warn().Err(err).Int("entries", len(jobs)).Int("halves", half).Msg("Chunk came back unusable, retrying it in halves")

	var translated int64
	firstCount, firstErr := translateJobs(ctx, provider, poFile, jobs[:half], path, nplurals, depth+1, fileLog)
	translated += firstCount
	secondCount, secondErr := translateJobs(ctx, provider, poFile, jobs[half:], path, nplurals, depth+1, fileLog)
	translated += secondCount

	switch {
	case firstErr != nil && secondErr != nil:
		return translated, fmt.Errorf("both halves failed: %w", firstErr)
	case firstErr != nil:
		return translated, fmt.Errorf("first half failed: %w", firstErr)
	case secondErr != nil:
		return translated, fmt.Errorf("second half failed: %w", secondErr)
	}
	return translated, nil
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

		// The po parser reads "#| msgid_plural" into PrevMsgId too, overwriting
		// the previous singular, so a plural entry's previous msgids can't be
		// compared. msgmerge's guess for a plural is usually a different string
		// with different placeholders, which msgfmt then rejects, so plurals are
		// always retranslated.
		if poFile.Messages[i].MsgIdPlural != "" {
			poFile.Messages[i].MsgStr = ""
			for j := range poFile.Messages[i].MsgStrPlural {
				poFile.Messages[i].MsgStrPlural[j] = ""
			}
		} else if currentMsgId != prevMsgId {
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

		// Check for conflicting translations among duplicates, plural forms
		// included
		firstMsgStr := ""
		var firstMsgStrPlural []string
		firstTranslation := ""
		for _, index := range indices {
			msg := poFile.Messages[index]
			translation := translationKey(msg)
			if translation == "" {
				continue
			}
			if firstTranslation != "" && translation != firstTranslation {
				return 0, false, fmt.Errorf("duplicate msgid '%s' (context: '%s') with conflicting msgstr: %q vs %q",
					poFile.Messages[indices[0]].MsgId, poFile.Messages[indices[0]].MsgContext, firstTranslation, translation)
			}
			firstTranslation = translation
			firstMsgStr = msg.MsgStr
			firstMsgStrPlural = msg.MsgStrPlural
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
		if firstTranslation != "" {
			poFile.Messages[keepIndex].MsgStrPlural = firstMsgStrPlural
		}
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

// pythonSpecPattern matches one Python %-conversion at the start of a string,
// named or positional, with any flags, width and precision.
var pythonSpecPattern = regexp.MustCompile(`^%(?:\([^)]*\))?[-#0 +]*(?:\*|\d+)?(?:\.(?:\*|\d+))?[diouxXeEfFgGcrsa]`)

// javascriptSpecPattern matches the placeholders Django's JS interpolate()
// substitutes. It gives no meaning to any other percent sign.
var javascriptSpecPattern = regexp.MustCompile(`%\(\w+\)s|%s`)

func hasFlag(msg po.Message, flag string) bool {
	for _, f := range msg.Comment.Flags {
		if f == flag {
			return true
		}
	}
	return false
}

// scanPythonPercents returns s's conversion specs in order and how many of its
// percent signs are neither a spec nor part of a %% escape.
func scanPythonPercents(s string) (specs []string, lone int) {
	for i := 0; i < len(s); {
		switch {
		case s[i] != '%':
			i++
		case strings.HasPrefix(s[i:], "%%"):
			i += 2
		default:
			if spec := pythonSpecPattern.FindString(s[i:]); spec != "" {
				specs = append(specs, spec)
				i += len(spec)
			} else {
				lone++
				i++
			}
		}
	}
	return specs, lone
}

// percentsEscaped reports whether msg's strings go through %-formatting, so a
// literal percent sign must be written %%. That is so for python-format entries
// and for Django template strings, whose msgids makemessages writes with %%.
func percentsEscaped(msg po.Message) bool {
	return hasFlag(msg, "python-format") || strings.Contains(msg.MsgId, "%%") || strings.Contains(msg.MsgIdPlural, "%%")
}

// escapeLonePercents doubles each percent sign in s that is not a %% escape or
// one of the msgid's own specs. A named spec the msgid lacks is left alone, so
// checkTranslation rejects it instead of it being hidden behind an escape.
func escapeLonePercents(s string, known map[string]bool) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		switch {
		case s[i] != '%':
			b.WriteByte(s[i])
			i++
		case strings.HasPrefix(s[i:], "%%"):
			b.WriteString("%%")
			i += 2
		default:
			spec := pythonSpecPattern.FindString(s[i:])
			if spec != "" && (known[spec] || strings.HasPrefix(spec, "%(")) {
				b.WriteString(spec)
				i += len(spec)
			} else if strings.HasPrefix(s[i:], "%(") {
				b.WriteByte('%')
				i++
			} else {
				b.WriteString("%%")
				i++
			}
		}
	}
	return b.String()
}

// fixMessagePercents makes the percent signs in msg's translations follow its
// msgid. Where the msgid is %-formatted, stray percent signs are escaped; where
// it holds a raw percent sign, %% escapes are undone, since nothing would turn
// them back into %. The msgid itself is never touched: it is the lookup key and
// has to match the source string exactly.
func fixMessagePercents(msg *po.Message) bool {
	var fixer func(string) string
	switch {
	case percentsEscaped(*msg):
		known := make(map[string]bool)
		for _, s := range []string{msg.MsgId, msg.MsgIdPlural} {
			specs, _ := scanPythonPercents(s)
			for _, spec := range specs {
				known[spec] = true
			}
		}
		fixer = func(s string) string { return escapeLonePercents(s, known) }
	case strings.Contains(msg.MsgId, "%") || strings.Contains(msg.MsgIdPlural, "%"):
		fixer = func(s string) string { return strings.ReplaceAll(s, "%%", "%") }
	default:
		return false
	}

	changed := false
	if msg.MsgStr != "" {
		if fixed := fixer(msg.MsgStr); fixed != msg.MsgStr {
			msg.MsgStr = fixed
			changed = true
		}
	}
	for i, s := range msg.MsgStrPlural {
		if fixed := fixer(s); fixed != s {
			msg.MsgStrPlural[i] = fixed
			changed = true
		}
	}
	return changed
}

func fixUnescapedPercents(poFile *po.File) (fixCount int, madeChanges bool) {
	for i := range poFile.Messages {
		if fixMessagePercents(&poFile.Messages[i]) {
			fixCount++
		}
	}
	return fixCount, fixCount > 0
}

// checkTranslation reports a translation of a python-format or
// javascript-format entry whose placeholders would fail msgfmt or break
// formatting at runtime: a spec the msgid doesn't have, a stray percent sign
// in a python-format string, or a spec the msgid needs that is missing. The
// singular form of a plural may leave out named specs, since languages often
// spell out "one"; positional specs must always line up. Empty forms are
// skipped, as completeness is isMessageTranslated's job.
func checkTranslation(msg po.Message, nplurals int) error {
	var scan func(string) ([]string, int)
	switch {
	case hasFlag(msg, "python-format"):
		scan = scanPythonPercents
	case hasFlag(msg, "javascript-format"):
		scan = func(s string) ([]string, int) { return javascriptSpecPattern.FindAllString(s, -1), 0 }
	default:
		return nil
	}

	idSpecs, _ := scan(msg.MsgId)
	pluralSpecs, _ := scan(msg.MsgIdPlural)
	known := make(map[string]bool)
	for _, spec := range append(append([]string{}, idSpecs...), pluralSpecs...) {
		known[spec] = true
	}

	type form struct {
		label    string
		text     string
		source   []string
		optional bool
	}
	var forms []form
	if msg.MsgIdPlural == "" {
		forms = append(forms, form{"msgstr", msg.MsgStr, idSpecs, false})
	} else {
		for i, s := range msg.MsgStrPlural {
			source := pluralSpecs
			if i == 0 && nplurals > 1 {
				source = idSpecs
			}
			forms = append(forms, form{fmt.Sprintf("msgstr[%d]", i), s, source, i == 0 && nplurals > 1})
		}
	}

	for _, f := range forms {
		if f.text == "" {
			continue
		}
		specs, lone := scan(f.text)
		if lone > 0 {
			return fmt.Errorf("%s has an unescaped %%", f.label)
		}
		present := make(map[string]bool)
		var positional []string
		for _, spec := range specs {
			if !known[spec] {
				return fmt.Errorf("%s has placeholder %s, which the msgid doesn't", f.label, spec)
			}
			present[spec] = true
			if !strings.HasPrefix(spec, "%(") {
				positional = append(positional, spec)
			}
		}

		var wantPositional []string
		for _, spec := range f.source {
			if !strings.HasPrefix(spec, "%(") {
				wantPositional = append(wantPositional, spec)
			} else if !present[spec] && !f.optional {
				return fmt.Errorf("%s is missing placeholder %s", f.label, spec)
			}
		}
		if strings.Join(positional, "\x00") != strings.Join(wantPositional, "\x00") {
			return fmt.Errorf("%s has positional placeholders %v, want %v", f.label, positional, wantPositional)
		}
	}
	return nil
}

// clearInvalidTranslations empties translations that checkTranslation rejects,
// so they are translated again instead of failing msgfmt.
func clearInvalidTranslations(poFile *po.File, fileLog *zerolog.Logger) (clearedCount int, madeChanges bool) {
	nplurals := getNPlurals(poFile.MimeHeader)
	for i := range poFile.Messages {
		msg := &poFile.Messages[i]
		err := checkTranslation(*msg, nplurals)
		if err == nil {
			continue
		}
		fileLog.Warn().Err(err).Str("msgid", msg.MsgId).Msg("Clearing translation with mismatched placeholders")
		msg.MsgStr = ""
		for j := range msg.MsgStrPlural {
			msg.MsgStrPlural[j] = ""
		}
		clearedCount++
	}
	return clearedCount, clearedCount > 0
}

// translationKey joins every form of msg's translation into one comparable
// string, empty when it has none.
func translationKey(msg po.Message) string {
	if msg.MsgStr == "" && strings.Join(msg.MsgStrPlural, "") == "" {
		return ""
	}
	return strings.Join(append([]string{msg.MsgStr}, msg.MsgStrPlural...), "\x00")
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

// formatHeader renders the PO header. gettext-go's Header.String() never writes
// Plural-Forms and writes unknown fields in map order, so both are appended
// here, the unknown fields sorted so saves are deterministic.
func formatHeader(header po.Header) string {
	unknownFields := header.UnknowFields
	header.UnknowFields = nil

	var buf bytes.Buffer
	buf.WriteString(header.String())
	if header.PluralForms != "" {
		fmt.Fprintf(&buf, `"%s: %s\n"`+"\n", "Plural-Forms", header.PluralForms)
	}

	keys := make([]string, 0, len(unknownFields))
	for k := range unknownFields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&buf, `"%s: %s\n"`+"\n", k, unknownFields[k])
	}
	return buf.String()
}

func savePoFile(poFile *po.File, path string) error {
	var buf bytes.Buffer
	buf.WriteString(formatHeader(poFile.MimeHeader))
	buf.WriteString("\n")

	for _, msg := range poFile.Messages {
		buf.WriteString(msg.String())
		buf.WriteString("\n")
	}

	return os.WriteFile(path, buf.Bytes(), 0644)
}
