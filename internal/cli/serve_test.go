package cli

import "testing"

func TestNewServeCmdExposesOpenAIStreamDefaultFlag(t *testing.T) {
	cmd := newServeCmd("test")
	if flag := cmd.Flags().Lookup("openai-stream-default"); flag == nil {
		t.Fatal("openai-stream-default flag is missing")
	}
	if flag := cmd.Flags().Lookup("desktop"); flag == nil {
		t.Fatal("desktop flag is missing")
	}
	if flag := cmd.Flags().Lookup("desktop-parent-pid"); flag == nil {
		t.Fatal("desktop-parent-pid flag is missing")
	}
}
