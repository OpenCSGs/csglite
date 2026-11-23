package cli

import "testing"

func TestFormatSpeed(t *testing.T) {
	cases := []struct {
		name string
		rate float64
		want string
	}{
		{name: "stalled", rate: 0, want: ""},
		{name: "below one byte per second", rate: 0.3, want: ""},
		{name: "bytes", rate: 512, want: "512 B/s"},
		{name: "kilobytes", rate: 1536, want: "1.5 KB/s"},
		{name: "megabytes", rate: 12.3 * 1024 * 1024, want: "12.3 MB/s"},
		{name: "gigabytes", rate: 1.5 * 1024 * 1024 * 1024, want: "1.5 GB/s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatSpeed(tc.rate); got != tc.want {
				t.Fatalf("formatSpeed(%v) = %q, want %q", tc.rate, got, tc.want)
			}
		})
	}
}
