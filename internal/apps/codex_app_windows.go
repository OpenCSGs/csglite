//go:build windows

package apps

import (
	"golang.org/x/sys/windows/registry"
)

var windowsUninstallRegistryPaths = []string{
	`Software\Microsoft\Windows\CurrentVersion\Uninstall`,
	`Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`,
}

// readWindowsCodexUninstallEntries scans the per-user and machine-wide
// registry Uninstall trees, which vendor installers populate regardless of
// where the user chose to install the app.
func readWindowsCodexUninstallEntries() []windowsUninstallEntry {
	var entries []windowsUninstallEntry
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		for _, path := range windowsUninstallRegistryPaths {
			entries = append(entries, readUninstallEntriesFromKey(root, path)...)
		}
	}
	return entries
}

func readUninstallEntriesFromKey(root registry.Key, path string) []windowsUninstallEntry {
	key, err := registry.OpenKey(root, path, registry.READ)
	if err != nil {
		return nil
	}
	defer key.Close()

	names, err := key.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}

	var entries []windowsUninstallEntry
	for _, name := range names {
		sub, err := registry.OpenKey(key, name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		entry := windowsUninstallEntry{
			DisplayName:     registryStringValue(sub, "DisplayName"),
			DisplayVersion:  registryStringValue(sub, "DisplayVersion"),
			InstallLocation: registryStringValue(sub, "InstallLocation"),
			DisplayIcon:     registryStringValue(sub, "DisplayIcon"),
		}
		sub.Close()
		if entry.DisplayName != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

func registryStringValue(key registry.Key, name string) string {
	value, _, err := key.GetStringValue(name)
	if err != nil {
		return ""
	}
	return value
}
