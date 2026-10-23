// Package scripts embeds the installer scripts so csghub-lite can reuse the
// same engine installation logic at runtime. See internal/upgrade's
// AlignLlamaRuntime, which runs the embedded script with --engine-only to align
// llama-server with the tag this binary is pinned to.
package scripts

import _ "embed"

// InstallSh is the POSIX installer, used on macOS and Linux.
//
//go:embed install.sh
var InstallSh string

// InstallPs1 is the Windows installer.
//
//go:embed install.ps1
var InstallPs1 string
