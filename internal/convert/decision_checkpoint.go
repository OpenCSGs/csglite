package convert

import (
	"os"
	"path/filepath"
)

// DecisionCheckpointArchitecture is the architecture the bundled converter
// gives a ModernBERT decision checkpoint such as convaiinnovations/laya.
const DecisionCheckpointArchitecture = "ModernBertDecisionModel"

// decisionCheckpointRootArchitectures lists the architectures the root
// config.json of decision checkpoints names. The bundled converter registers
// none of them: it recognizes these repos by their layout (see
// IsDecisionCheckpoint) and converts them as DecisionCheckpointArchitecture.
// They are listed so marketplace listings, which only see the root config,
// report the model as convertible; do not drop them when syncing
// pythonConverterArchitectureMapping with the converter.
var decisionCheckpointRootArchitectures = map[string]string{
	"LayaTypedDecisions": "modern-bert",
}

// IsDecisionCheckpointRootArchitecture reports whether arch is the root
// architecture of a decision checkpoint. On disk such a model converts only
// with the checkpoint layout; without it the converter cannot load it.
func IsDecisionCheckpointRootArchitecture(arch string) bool {
	_, ok := decisionCheckpointRootArchitectures[arch]
	return ok
}

// IsDecisionCheckpoint reports whether modelDir holds a ModernBERT decision
// checkpoint: an encoder sub-directory next to the decision head config. The
// root config.json of these repos names an architecture of its own (Laya uses
// "LayaTypedDecisions"), so the converter recognizes them by this layout; this
// mirrors _is_decision_checkpoint() in the bundled conversion/bert.py.
func IsDecisionCheckpoint(modelDir string) bool {
	if !isFile(filepath.Join(modelDir, "encoder", "config.json")) {
		return false
	}
	return isFile(filepath.Join(modelDir, "rl_agent_config.json")) || isFile(filepath.Join(modelDir, "julia_config.json"))
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
