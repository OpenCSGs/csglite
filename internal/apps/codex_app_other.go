//go:build !windows

package apps

func readWindowsCodexUninstallEntries() []windowsUninstallEntry {
	return nil
}
