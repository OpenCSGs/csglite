//go:build windows

package inference

import "testing"

func assertProcessGone(t *testing.T, _ int) {
	t.Helper()
	t.Skip("process-group assertion is Unix-specific")
}
