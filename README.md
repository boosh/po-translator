# PO File AI Translation Tool

A Go CLI tool that manages Django/gettext `.po` file translations using AI services. The tool processes translation files in parallel, cleans up fuzzy markers, and translates untranslated strings using configurable AI providers.

## Features

- **Parallel Processing:** Translates multiple `.po` files concurrently for maximum speed.
- **Fuzzy Entry Handling:** Automatically clears `fuzzy` flags and their incorrect translations.
- **AI-Powered Translations:** Identifies and translates empty `msgstr` entries using AI.
- **Configurable Providers:** Supports multiple AI providers (currently Google Gemini, with a stub for Anthropic).
- **Structured Logging:** Detailed, leveled logging with `zerolog` to stderr or a file.
- **Flexible Configuration:** Configure providers, models, API keys, and more via CLI flags or a `.env` file.
- **Dry Run Mode:** Preview changes without writing to any files.
- **Strict Mode:** Exit immediately on the first error for CI/CD pipelines.

## Installation

1.  **Prerequisites:**
    *   Go 1.21+ installed.
    *   An API key for your chosen AI provider (e.g., Google AI Studio).

2.  **Clone the repository:**
    ```bash
    git clone https://github.com/your-username/po-translator.git
    cd po-translator
    ```

3.  **Build from source:**
    ```bash
    go build -o po-translator .
    ```
    This will create a `po-translator` executable in the current directory.

4.  **Install (optional):**
    To make the tool available system-wide, you can install it to your Go bin directory:
    ```bash
    go install .
    ```
    Ensure that `$(go env GOPATH)/bin` is in your system's `PATH`.

## Quick Start

1.  **Set up your environment:**
    Create a `.env` file in the project root and add your API key. For example, for Google Gemini:
    ```
    # .env
    GOOGLE_API_KEY=your_google_api_key_here
    ```
    Alternatively, you can use the `--api-key` flag.

2.  **Run a translation:**
    Point the tool at your `.po` files using a glob pattern. The following command will find all `.po` files in the `locale/` directory, clear fuzzy entries, and translate any untranslated strings using Google's `gemini-1.5-flash` model.

    ```bash
    ./po-translator --provider=google --model=gemini-1.5-flash "locale/**/*.po"
    ```

3.  **Perform a dry run:**
    To see what the tool *would* do without making any changes, use the `--dry-run` flag.

    ```bash
    ./po-translator --provider=google --model=gemini-1.5-flash --dry-run "locale/**/*.po"
    ```

## CLI Reference

### Usage
```
po-translator [flags] <glob-pattern...>
```

### Arguments
-   `<glob-pattern...>`: One or more glob patterns to find `.po` files (e.g., `"locale/*/LC_MESSAGES/*.po"`).

### Flags

| Flag              | Type     | Default | Description                                                   |
| ----------------- | -------- | ------- | ------------------------------------------------------------- |
| `--provider`      | `string` |         | **(Required)** AI provider to use: `google`.                  |
| `--model`         | `string` |         | **(Required)** Model name to use for translation.             |
| `--api-key`       | `string` |         | API key for the provider. Overrides environment variables.    |
| `--chunk-size`    | `int`    | `50`    | Number of entries to translate per AI request.                |
| `--dry-run`       | `bool`   | `false` | Process files but do not write any changes.                   |
| `--log-file`      | `string` |         | Path to log file for output. Defaults to stderr.              |
| `--log-level`     | `string` | `info`  | Log level (`debug`, `info`, `warn`, `error`).                 |
| `--max-retries`   | `int`    | `3`     | Max retries for failed API calls.                             |
| `--retry-delay`   | `duration`| `2s`   | Delay between retries (e.g., `2s`, `500ms`).                   |
| `--strict`        | `bool`   | `false` | Exit immediately on any error.                                |
| `--temperature`   | `float`  | `0.3`   | Temperature for AI generation (0.0 to 1.0).                   |

## AI Provider Setup

### Google Gemini
-   **Provider Name:** `google`
-   **Environment Variable:** `GOOGLE_API_KEY` or `GEMINI_API_KEY`
-   **Models:** `gemini-1.5-flash`, `gemini-1.5-pro`, etc.

### Anthropic Claude
-   _(Currently stubbed out and disabled)_
-   **Provider Name:** `anthropic`
-   **Environment Variable:** `ANTHROPIC_API_KEY`

## Development

### Building
```bash
go build -o po-translator .
```

### Running Tests
```bash
go test ./...
```