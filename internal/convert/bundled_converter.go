package convert

import (
	"io/fs"

	llamacppassets "github.com/opencsgs/llama-cpp-assets"
)

var bundledConverterPy, _ = fs.ReadFile(llamacppassets.FS, llamacppassets.ConverterPath)

var bundledGGUFPy = llamacppassets.FS
var bundledConversion = llamacppassets.FS

const bundledGGUFPyRoot = llamacppassets.GGUFPyRoot
const bundledConversionRoot = llamacppassets.ConversionRoot

// These aliases keep converter cache and error reporting tied directly to the
// versioned dependency that owns the Python assets.
const (
	bundledConverterRevision    = llamacppassets.Revision
	BundledConverterLLamacppRef = llamacppassets.LlamaCppRef
)
