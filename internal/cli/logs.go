package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	var count int
	var follow bool
	cmd := &cobra.Command{
		Use:     "logs [MODEL]",
		Short:   "Show the last matching lines of the local server log",
		Example: "  csghub-lite logs Qwen/Qwen-Image-2.1\n  csghub-lite logs -f -n 20",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if count < 0 {
				return fmt.Errorf("--tail must be non-negative")
			}
			path, err := config.ServerLogPath()
			if err != nil {
				return err
			}
			var match func(string) bool
			if len(args) > 0 {
				if strings.TrimSpace(args[0]) == "" {
					return fmt.Errorf("model ID is empty")
				}
				cfg, err := config.Load()
				if err != nil {
					return err
				}
				mgr := model.NewManager(cfg)
				ids := []string{args[0]}
				// Keep historical full IDs usable after the model is removed.
				if lm, err := mgr.ResolveLocalModel(args[0]); err == nil {
					ids = append(ids, lm.FullName())
					if public, err := mgr.PublicModelID(lm.FullName()); err == nil {
						ids = append(ids, public)
					}
				}
				match = modelLogFilter(ids)
			}
			return readServerLog(cmd.Context(), cmd.OutOrStdout(), path, match, count, follow)
		},
	}
	cmd.Flags().IntVarP(&count, "tail", "n", 50, "number of matching historical lines")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "continue printing new matching lines")
	return cmd
}

// Check for a literal ID before doing the more expensive boundary match.
func modelLogFilter(ids []string) func(string) bool {
	terms := make([]string, 0, len(ids))
	for _, id := range ids {
		terms = append(terms, regexp.QuoteMeta(id))
	}
	pattern := regexp.MustCompile("(^|[^[:alnum:]_./-])(" + strings.Join(terms, "|") + ")($|[^[:alnum:]_./-])")
	return func(line string) bool {
		for _, id := range ids {
			if strings.Contains(line, id) {
				return pattern.MatchString(line)
			}
		}
		return false
	}
}

// Search increasingly large suffixes so normal tail operations do not need to
// scan a multi-GB log. Each pass retains only the last count matches.
func logHistory(ctx context.Context, file *os.File, end int64, match func(string) bool, count int, follow bool) (lines []string, partial string, err error) {
	if count == 0 && !follow {
		return nil, "", nil
	}
	for window := int64(64 << 10); ; window *= 2 {
		start := max(int64(0), end-window)
		reader := bufio.NewReader(io.NewSectionReader(file, start, end-start))
		if start > 0 {
			// The first line may start in the preceding window.
			if _, err := reader.ReadString('\n'); err == io.EOF {
				continue
			} else if err != nil {
				return nil, "", err
			}
		}
		lines = nil
		next := 0
		for {
			if err := ctx.Err(); err != nil {
				return nil, "", err
			}
			line, err := reader.ReadString('\n')
			if err != nil && err != io.EOF {
				return nil, "", err
			}
			if err == io.EOF && follow {
				partial = line
				break
			}
			if count > 0 && len(line) > 0 && (match == nil || match(line)) {
				if len(lines) < count {
					lines = append(lines, line)
				} else {
					lines[next] = line
					next = (next + 1) % count
				}
			}
			if err == io.EOF {
				break
			}
		}
		if start == 0 || len(lines) == count {
			return append(lines[next:], lines[:next]...), partial, nil
		}
	}
}

func readServerLog(ctx context.Context, out io.Writer, path string, match func(string) bool, count int, follow bool) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening server log (file logging must have been enabled): %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	offset := info.Size()
	lines, pending, err := logHistory(ctx, file, offset, match, count, follow)
	if err != nil {
		return err
	}
	for _, line := range lines {
		if _, err := io.WriteString(out, line); err != nil {
			return err
		}
	}
	if !follow {
		return nil
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		current, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !os.SameFile(info, current) {
			replacement, err := os.Open(path)
			if err != nil {
				return err
			}
			_ = file.Close()
			file = replacement
			info, err = file.Stat()
			if err != nil {
				return err
			}
			current = info
			offset, pending = 0, ""
		}
		if current.Size() < offset {
			offset, pending = 0, ""
		}
		if current.Size() == offset {
			continue
		}
		r := bufio.NewReader(io.NewSectionReader(file, offset, current.Size()-offset))
		for {
			part, err := r.ReadString('\n')
			offset += int64(len(part))
			if err == io.EOF {
				pending += part
				break
			}
			if err != nil {
				return err
			}
			line := pending + part
			pending = ""
			if match == nil || match(line) {
				if _, err := io.WriteString(out, line); err != nil {
					return err
				}
			}
		}
	}
}
