# Go Conventions

## Project Structure

- CLI commands: `internal/cli/` - one file per command, register in `root.go`.
- Config: `internal/config/` - app home is `~/.csghub-lite/`.
- Inference engine: `internal/inference/` - manages the `llama-server`
  subprocess.
- Model management: `internal/model/` - local model storage and metadata.
- API server: `internal/server/` - HTTP handlers and routes.

## CLI Commands

- Use `cobra.Command` for all CLI commands.
- Each command file exports a `newXxxCmd()` function returning `*cobra.Command`.
- Register new commands in `internal/cli/root.go` via `cmd.AddCommand()`.

## Error Handling

- Wrap errors with context: `fmt.Errorf("doing X: %w", err)`.
- CLI `RunE` functions return errors; cobra handles printing.
- Never silently swallow errors unless intentional; document why when it is.

## Dependencies

- Config is loaded via `config.Load()` using a singleton with `sync.Once`.
- Model operations go through `model.NewManager(cfg)`.
- Inference uses `inference.LoadEngineWithProgress()` or `newLlamaEngine()`.

## Lint

- Local and CI lint use golangci-lint `v1.64.8` via `make lint`.
- Install the repo pre-commit hook with `make hooks`. It runs `make lint`
  before each commit. Set `SKIP_LINT=1` only when you must bypass it.
