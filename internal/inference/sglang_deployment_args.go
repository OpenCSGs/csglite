package inference

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ParseSGLangDeploymentArgs accepts tuning flags from a deployment controller.
// Workers validate the same allowlist before executing any received arguments.
func ParseSGLangDeploymentArgs(raw string) ([]string, error) {
	if len(raw) > 2048 {
		return nil, fmt.Errorf("SGLang arguments must fit in 2048 bytes")
	}
	var args []string
	seen := map[string]bool{}
	fields := strings.Fields(raw)
	for len(fields) > 0 {
		flag, value, inline := strings.Cut(fields[0], "=")
		fields = fields[1:]
		if !inline && len(fields) > 0 && !strings.HasPrefix(fields[0], "--") {
			value, fields = fields[0], fields[1:]
		}
		if seen[flag] {
			return nil, fmt.Errorf("duplicate SGLang argument %s", flag)
		}
		seen[flag] = true
		valid := false
		switch flag {
		case "--chunked-prefill-size", "--cuda-graph-max-bs-decode", "--speculative-num-steps",
			"--speculative-eagle-topk", "--speculative-num-draft-tokens":
			n, err := strconv.Atoi(value)
			valid = err == nil && (n > 0 || (flag == "--chunked-prefill-size" && n == -1))
		case "--max-total-tokens":
			_, err := parseSGLangTokenCount(value)
			valid = err == nil
		case "--mem-fraction-static":
			fraction, err := strconv.ParseFloat(value, 64)
			valid = err == nil && !math.IsNaN(fraction) && fraction > 0 && fraction <= 1
		case "--speculative-algorithm":
			valid = value == "NEXTN" || value == "EAGLE" || value == "none"
		case "--kv-cache-dtype":
			valid = value == "auto" || value == "bf16" || value == "bfloat16" || value == "fp8_e4m3" || value == "fp8_e5m2"
		case "--mamba-ssm-dtype":
			valid = value == "float32" || value == "bfloat16" || value == "float16"
		case "--mamba-radix-cache-strategy":
			valid = value == "auto" || value == "extra_buffer" || value == "no_buffer"
		case "--tool-call-parser":
			parser, _, _ := validateSGLangToolParser(value)
			valid = parser != ""
		case "--reasoning-parser":
			_, err := validateSGLangReasoningParser(value)
			valid = err == nil
		default:
			return nil, fmt.Errorf("unsupported SGLang deployment argument %q", flag)
		}
		if !valid {
			return nil, fmt.Errorf("invalid value for SGLang deployment argument %s", flag)
		}
		args = append(args, flag, value)
	}
	return args, nil
}

func mergeSGLangDeploymentArgs(node, deployment []string) []string {
	override := sglangFlagSet(deployment)
	algorithm, _ := sglangFlagValue(deployment, "--speculative-algorithm")
	disabled := algorithm == "none"
	var merged []string
	for i := 0; i < len(node); i++ {
		flag, _, inline := strings.Cut(node[i], "=")
		if !override[flag] && !(disabled && strings.HasPrefix(flag, "--speculative-")) {
			merged = append(merged, node[i])
			continue
		}
		if !inline && i+1 < len(node) && !strings.HasPrefix(node[i+1], "--") {
			i++
		}
	}
	for i := 0; i < len(deployment); i += 2 {
		if disabled && strings.HasPrefix(deployment[i], "--speculative-") {
			continue
		}
		merged = append(merged, deployment[i:i+2]...)
	}
	return merged
}
