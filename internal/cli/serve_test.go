package cli

import "testing"

func TestNewServeCmdExposesOpenAIStreamDefaultFlag(t *testing.T) {
	cmd := newServeCmd("test")
	if flag := cmd.Flags().Lookup("openai-stream-default"); flag == nil {
		t.Fatal("openai-stream-default flag is missing")
	}
}
