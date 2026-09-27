package models

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

var (
	hfRevision = regexp.MustCompile(`^[0-9a-f]{40}$`)
	hfRepo     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)
	ollamaTag  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*:[A-Za-z0-9._-]+$`)
)

// SafeFormats are the only weight formats Rigfile will download (RIGFILE_PLAN.md §9.5): none of them can execute code when
// loaded. Pickle-based files (`.bin`, `.pt`, `.pkl`) and models that need `trust_remote_code` are never applied.
var SafeFormats = map[string]bool{"safetensors": true, "gguf": true, "mlx-safetensors": true}

// deniedArgs may not be set through a rig's `args` (they are structured elsewhere, or dangerous).
var deniedArgs = map[string]bool{"host": true, "port": true, "model": true, "trust-remote-code": true, "trust_remote_code": true, "adapter-path": true}

// AppliedEngines are the engines Rigfile can install and run. llama.cpp and vLLM appear in the schema and the catalog but
// nothing was researched for them, so a variant that names them is never applied.
var AppliedEngines = map[string]bool{"ollama": true, "mlx-lm": true}

// IsLoopbackHost reports whether a listen host is loopback only.
func IsLoopbackHost(h string) bool {
	h = strings.Trim(h, "[]")
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Validate says whether a variant may be applied. The rules are the plan's model security rules plus "never apply what
// was not researched".
func Validate(v Variant) error {
	switch {
	case v.Status == "stub":
		return errors.New("a catalog placeholder that was never researched; it is never applied")
	case placeholder(v.Model) || placeholder(v.Revision) || placeholder(v.EngineVersion):
		return errors.New("has TODO values in the catalog; it is never applied")
	case v.Engine == "":
		return errors.New("names no engine")
	case !AppliedEngines[v.Engine]:
		return fmt.Errorf("the %s engine is not supported yet (nothing about it was verified)", v.Engine)
	case v.Model == "":
		return errors.New("names no model")
	}
	if v.WeightsFormat != "" && !SafeFormats[v.WeightsFormat] {
		return fmt.Errorf("weights format %q is not a safe format (safetensors, gguf, mlx-safetensors)", v.WeightsFormat)
	}
	for k := range v.Args {
		if deniedArgs[strings.ToLower(k)] {
			return fmt.Errorf("the flag --%s may not be set here (host, port and model are structured; trust-remote-code is never allowed)", k)
		}
	}
	if h := v.Serve.Host; h != "" && !IsLoopbackHost(h) {
		return fmt.Errorf("serves on %s: model servers listen on loopback only", h)
	}
	switch v.Engine {
	case "mlx-lm":
		if !hfRepo.MatchString(v.Model) {
			return fmt.Errorf("model %q is not a Hugging Face repo id (org/name)", v.Model)
		}
		if !hfRevision.MatchString(v.Revision) {
			return errors.New("a Hugging Face model must be pinned to a 40-character commit revision")
		}
		if v.EngineVersion == "" {
			return errors.New("the engine version must be pinned")
		}
	case "ollama":
		if !ollamaTag.MatchString(v.Model) {
			return fmt.Errorf("model %q is not an Ollama tag (name:tag)", v.Model)
		}
	}
	return nil
}
