package cli

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// Stream large weights or audio recordings one file at a time. A rejected
// request also unblocks the producer when the server stops consuming the form.
func postMultipart(cmd *cobra.Command, path string, write func(*multipart.Writer) error) (*http.Response, error) {
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	contentType := form.FormDataContentType()
	finished := make(chan error, 1)
	go func() {
		err := write(form)
		if err == nil {
			err = form.Close()
		}
		_ = writer.CloseWithError(err)
		finished <- err
	}()
	resp, err := localRequest(cmd, http.MethodPost, path, contentType, reader)
	_ = reader.Close()
	writeErr := <-finished
	if err != nil {
		return nil, err
	}
	if writeErr != nil {
		resp.Body.Close()
		return nil, writeErr
	}
	return resp, nil
}

func writeFormFile(form *multipart.Writer, field, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("expected a regular file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	part, err := form.CreateFormFile(field, filepath.Base(path))
	if err != nil {
		return err
	}
	_, err = io.Copy(part, file)
	return err
}
