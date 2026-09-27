package platform

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// GPU is one graphics device. Vendor is "nvidia", "apple" or "" (not detected; only these two are probed).
type GPU struct {
	Vendor string
	Name   string
	VRAMGB float64 // Apple Silicon: the unified memory, which the GPU shares
}

// Hardware is what model selection needs to know about the machine (RIGFILE_PLAN.md §9.5).
type Hardware struct {
	OS         string // macos, linux, windows
	Arch       string // arm64, amd64
	MemoryGB   float64
	GPUs       []GPU
	FreeDiskGB float64
}

// HasGPU reports whether a GPU of the vendor exists, and the most VRAM any of them has.
func (h Hardware) HasGPU(vendor string) (bool, float64) {
	var best float64
	found := false
	for _, g := range h.GPUs {
		if g.Vendor == vendor {
			found = true
			if g.VRAMGB > best {
				best = g.VRAMGB
			}
		}
	}
	return found, best
}

// HWProbe is how hardware is read. Tests replace every field; RealProbe fills them in.
type HWProbe struct {
	GOOS, GOARCH string
	Run          func(name string, args ...string) (string, error)
	ReadFile     func(path string) (string, error)
	FreeBytes    func(path string) (uint64, error)
	Path         string // where the model cache will live (free space is measured on its volume)
}

const gib = 1 << 30

// DetectHardware reads what it can; anything it cannot read is left zero (selection then treats a `min_*` requirement as
// unmet, never as met).
func DetectHardware(p HWProbe) Hardware {
	h := Hardware{Arch: p.GOARCH}
	switch p.GOOS {
	case "darwin":
		h.OS = "macos"
		if out, err := p.Run("sysctl", "-n", "hw.memsize"); err == nil {
			h.MemoryGB = bytesToGB(parseUint(out))
		}
		// a translated (Rosetta) x86-64 process on Apple Silicon still runs on an arm64 machine
		if p.GOARCH == "amd64" {
			if out, err := p.Run("sysctl", "-n", "sysctl.proc_translated"); err == nil && strings.TrimSpace(out) == "1" {
				h.Arch = "arm64"
			}
		}
		if h.Arch == "arm64" {
			h.GPUs = []GPU{{Vendor: "apple", Name: "Apple Silicon", VRAMGB: h.MemoryGB}}
		}
	case "linux":
		h.OS = "linux"
		if b, err := p.ReadFile("/proc/meminfo"); err == nil {
			h.MemoryGB = parseMemInfo(b)
		}
	case "windows":
		h.OS = "windows"
		if out, err := p.Run("powershell", "-NoProfile", "-NonInteractive", "-Command", "(Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory"); err == nil {
			h.MemoryGB = bytesToGB(parseUint(out))
		}
	}
	if p.GOOS == "linux" || p.GOOS == "windows" {
		if out, err := p.Run("nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader,nounits"); err == nil {
			h.GPUs = append(h.GPUs, parseNvidiaSmi(out)...)
		}
	}
	if p.FreeBytes != nil && p.Path != "" {
		if n, err := p.FreeBytes(p.Path); err == nil {
			h.FreeDiskGB = float64(n) / gib
		}
	}
	return h
}

func bytesToGB(n uint64) float64 { return float64(n) / gib }

func parseUint(s string) uint64 {
	n, _ := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	return n
}

// parseMemInfo reads MemTotal (in kB) from /proc/meminfo.
func parseMemInfo(s string) float64 {
	for _, line := range strings.Split(s, "\n") {
		if rest, ok := strings.CutPrefix(line, "MemTotal:"); ok {
			f := strings.Fields(rest)
			if len(f) >= 1 {
				kb, _ := strconv.ParseUint(f[0], 10, 64)
				return float64(kb) * 1024 / gib
			}
		}
	}
	return 0
}

// parseNvidiaSmi reads `name, memory.total` lines (memory in MiB).
func parseNvidiaSmi(s string) []GPU {
	var out []GPU
	for _, line := range strings.Split(s, "\n") {
		name, mem, ok := strings.Cut(strings.TrimSpace(line), ",")
		if !ok {
			continue
		}
		mib, err := strconv.ParseFloat(strings.TrimSpace(mem), 64)
		if err != nil {
			continue
		}
		out = append(out, GPU{Vendor: "nvidia", Name: strings.TrimSpace(name), VRAMGB: mib / 1024})
	}
	return out
}

// RealProbe reads this machine. Commands are bounded in time and their absence is not an error.
func RealProbe(goos, goarch, cachePath string) HWProbe {
	return HWProbe{
		GOOS: goos, GOARCH: goarch, Path: cachePath,
		Run: func(name string, args ...string) (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, name, args...).Output()
			return string(out), err
		},
		ReadFile:  readFileString,
		FreeBytes: freeBytes,
	}
}
