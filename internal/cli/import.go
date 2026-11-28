package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"mime/multipart"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func newImportCmd() *cobra.Command {
	var overwrite bool
	cmd := &cobra.Command{
		Use:     "import MODEL PATH",
		Short:   "Import a model directory, archive, or single weights file",
		Example: "  csghub-lite import local/my-model ./my-model",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(args[0]) == "" {
				return fmt.Errorf("model ID is required")
			}
			if args[1] == "" {
				return fmt.Errorf("model path is required")
			}
			path := filepath.Clean(args[1])
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			mode := "files"
			switch {
			case info.IsDir():
				mode = "directory"
			case !info.Mode().IsRegular():
				return fmt.Errorf("expected a regular file or directory: %s", path)
			case isModelArchive(path):
				mode = "archive"
			}
			resp, err := postMultipart(cmd, "/api/models/upload", func(form *multipart.Writer) error {
				if err := form.WriteField("model", args[0]); err != nil {
					return err
				}
				if err := form.WriteField("mode", mode); err != nil {
					return err
				}
				if err := form.WriteField("overwrite", strconv.FormatBool(overwrite)); err != nil {
					return err
				}
				if mode != "directory" {
					return writeFormFile(form, "files", path)
				}
				return filepath.WalkDir(path, func(current string, d fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if d.IsDir() {
						if current != path && d.Name() == ".git" {
							return filepath.SkipDir
						}
						return nil
					}
					if !d.Type().IsRegular() {
						return fmt.Errorf("unsupported file type: %s", current)
					}
					relative, err := filepath.Rel(path, current)
					if err != nil {
						return err
					}
					if err := form.WriteField("paths", filepath.ToSlash(relative)); err != nil {
						return err
					}
					return writeFormFile(form, "files", current)
				})
			})
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			var result json.RawMessage
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				return fmt.Errorf("decoding model import response: %w", err)
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		},
	}
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace an existing model of this ID")
	return cmd
}

func isModelArchive(path string) bool {
	name := strings.ToLower(path)
	return strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".tar") || strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz")
}
