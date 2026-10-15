package convert

import (
	"os"
	"path/filepath"
)

// DecisionCheckpointArchitecture is the architecture the bundled converter
// gives a ModernBERT decision checkpoint such as convaiinnovations/laya.
const DecisionCheckpointArchitecture = "ModernBertDecisionModel"

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
