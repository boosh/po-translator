package cmd

import (
	"context"
	"fmt"
	"os"
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

	madeChanges := false

	// Step 1: Clear fuzzy flags
	fuzzyCount := 0
	for i := range poFile.Messages {
		if poFile.Messages[i].Comment.GetFuzzy() {
			poFile.Messages[i].Comment.SetFuzzy(false)
			poFile.Messages[i].MsgStr = ""
			fuzzyCount++
			madeChanges = true
		}
	}
	if fuzzyCount > 0 {
		fileLog.Info().Int("count", fuzzyCount).Msg("Cleared fuzzy entries")
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

	// Step 3: Translate in chunks
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
			return 0, fmt.Errorf("translation error in chunk %d-%d: %w", i+1, end, err)
		}
		chunkLog.Debug().Float64("duration_seconds", time.Since(chunkStart).Seconds()).Msg("Chunk translation took")

		for j, translation := range translations {
			originalIndex := jobChunk[j].Index
			poFile.Messages[originalIndex].MsgStr = translation
		}
		totalTranslated += int64(len(translations))
		madeChanges = true
	}

	// Step 4: Save the file
	if !dryRun && madeChanges {
		if err := poFile.Save(path); err != nil {
			return 0, fmt.Errorf("failed to save translated file: %w", err)
		}
	}

	fileLog.Info().Float64("duration_seconds", time.Since(start).Seconds()).Msg("Completed processing file")
	return totalTranslated, nil
}