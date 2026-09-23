package inference

import "testing"

func TestParseLlamaServerVersion(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string
	}{
		{
			name: "dev build",
			out:  "version: 0.1.2-dev (build 10549, commit b2e5e9b28)\nbuilt with AppleClang 21.0.0.21000101 for Darwin arm64\n",
			want: "0.1.2-dev (build 10549, commit b2e5e9b28)",
		},
		{
			name: "numeric build",
			out:  "version: 4868 (c2d1a7f83)\nbuilt with cc\n",
			want: "4868 (c2d1a7f83)",
		},
		{
			name: "no version line",
			out:  "llama.cpp\n",
			want: "",
		},
		{
			name: "empty output",
			out:  "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseLlamaServerVersion(tt.out); got != tt.want {
				t.Fatalf("parseLlamaServerVersion(%q) = %q, want %q", tt.out, got, tt.want)
			}
		})
	}
}
