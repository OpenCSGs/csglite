package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func isPublishedGatewayRequest(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, gatewayPrefix+"/v1/")
}

// Rewrite only the response metadata; generated content and provider extensions
// remain raw JSON. A service's public identity must not depend on its replica.
func rewriteResponseModel(body []byte, model string) []byte {
	var payload map[string]json.RawMessage
	if json.Unmarshal(body, &payload) != nil || payload == nil || payload["error"] != nil {
		return body
	}
	payload["model"], _ = json.Marshal(model)
	result, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return result
}

func copyGatewayModelStream(dst io.Writer, src io.Reader, model string) error {
	reader := bufio.NewReader(src)
	for {
		line, readErr := reader.ReadBytes('\n')
		if bytes.HasPrefix(line, []byte("data:")) {
			data := bytes.TrimSpace(line[len("data:"):])
			updated := rewriteResponseModel(data, model)
			if !bytes.Equal(data, updated) {
				line = append(append([]byte("data: "), updated...), '\n')
			}
		}
		if len(line) > 0 {
			n, err := dst.Write(line)
			if err != nil {
				return err
			}
			if n != len(line) {
				return io.ErrShortWrite
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			return readErr
		}
	}
}
