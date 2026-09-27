package models

import (
	"fmt"
	"sort"

	"github.com/rigfile/rigfile/internal/platform"
)

// Matches reports whether a variant's `when` holds for this machine. Recognised keys: os, arch, gpu (nvidia|apple),
// min_memory_gb, min_vram_gb, min_disk_gb. An unknown key never matches (a condition Rigfile cannot evaluate must not be
// read as satisfied), and a `min_*` requirement is unmet when the machine's value could not be read.
func Matches(when map[string]any, hw platform.Hardware) (bool, string) {
	keys := make([]string, 0, len(when))
	for k := range when {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := when[k]
		switch k {
		case "os":
			if s, _ := v.(string); s != hw.OS {
				return false, fmt.Sprintf("needs os %v, this is %s", v, hw.OS)
			}
		case "arch":
			if s, _ := v.(string); s != hw.Arch {
				return false, fmt.Sprintf("needs arch %v, this is %s", v, hw.Arch)
			}
		case "gpu":
			s, _ := v.(string)
			if ok, _ := hw.HasGPU(s); !ok {
				return false, fmt.Sprintf("needs a %v GPU, none found", v)
			}
		case "min_memory_gb":
			if want, ok := num(v); !ok || hw.MemoryGB < want {
				return false, fmt.Sprintf("needs %v GB of memory, this has %.0f GB", v, hw.MemoryGB)
			}
		case "min_vram_gb":
			want, ok := num(v)
			best := 0.0
			for _, g := range hw.GPUs {
				if g.VRAMGB > best {
					best = g.VRAMGB
				}
			}
			if !ok || best < want {
				return false, fmt.Sprintf("needs %v GB of GPU memory, this has %.0f GB", v, best)
			}
		case "min_disk_gb":
			if want, ok := num(v); !ok || hw.FreeDiskGB < want {
				return false, fmt.Sprintf("needs %v GB of free disk, this has %.0f GB", v, hw.FreeDiskGB)
			}
		default:
			return false, fmt.Sprintf("unknown condition %q", k)
		}
	}
	return true, ""
}

func num(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// Rejected is a variant that was looked at and not chosen, and why (shown on the plan screen).
type Rejected struct {
	Label  string
	Reason string
}

func label(v Variant, i int) string {
	switch {
	case v.ID != "":
		return v.ID
	case v.Model != "":
		return fmt.Sprintf("%s (%s)", v.Model, v.Engine)
	}
	return fmt.Sprintf("variant %d", i+1)
}

// Choose returns the first variant that matches the hardware AND is safe to apply (Validate), plus every variant it passed
// over with the reason. The person can still pick another on the plan screen; this is the default.
func Choose(vs []Variant, hw platform.Hardware) (*Variant, []Rejected) {
	var rejected []Rejected
	for i := range vs {
		v := vs[i]
		if ok, why := Matches(v.When, hw); !ok {
			rejected = append(rejected, Rejected{label(v, i), why})
			continue
		}
		if err := Validate(v); err != nil {
			rejected = append(rejected, Rejected{label(v, i), err.Error()})
			continue
		}
		return &v, rejected
	}
	return nil, rejected
}
