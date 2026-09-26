package platform

import (
	"errors"
	"testing"
)

func fakeProbe(goos, goarch string, cmds map[string]string, files map[string]string) HWProbe {
	return HWProbe{
		GOOS: goos, GOARCH: goarch, Path: "/cache",
		Run: func(name string, args ...string) (string, error) {
			key := name
			for _, a := range args {
				key += " " + a
			}
			if v, ok := cmds[key]; ok {
				return v, nil
			}
			return "", errors.New("not found")
		},
		ReadFile: func(p string) (string, error) {
			v, ok := files[p]
			if !ok {
				return "", errors.New("no file")
			}
			return v, nil
		},
		FreeBytes: func(string) (uint64, error) { return 100 << 30, nil },
	}
}

func TestDetectAppleSilicon(t *testing.T) {
	h := DetectHardware(fakeProbe("darwin", "arm64", map[string]string{"sysctl -n hw.memsize": "17179869184\n"}, nil))
	if h.OS != "macos" || h.Arch != "arm64" || h.MemoryGB != 16 || h.FreeDiskGB != 100 {
		t.Fatalf("%+v", h)
	}
	if ok, vram := h.HasGPU("apple"); !ok || vram != 16 {
		t.Fatalf("unified memory is the GPU's memory: %+v", h.GPUs)
	}
}

func TestRosettaOnAppleSiliconIsStillArm64(t *testing.T) {
	h := DetectHardware(fakeProbe("darwin", "amd64", map[string]string{"sysctl -n hw.memsize": "34359738368", "sysctl -n sysctl.proc_translated": "1\n"}, nil))
	if h.Arch != "arm64" || h.MemoryGB != 32 {
		t.Fatalf("%+v", h)
	}
	// a real Intel Mac: no translation, no Apple GPU
	h = DetectHardware(fakeProbe("darwin", "amd64", map[string]string{"sysctl -n hw.memsize": "17179869184", "sysctl -n sysctl.proc_translated": "0\n"}, nil))
	if h.Arch != "amd64" || len(h.GPUs) != 0 {
		t.Fatalf("%+v", h)
	}
}

func TestDetectLinuxWithNvidia(t *testing.T) {
	files := map[string]string{"/proc/meminfo": "MemTotal:       32858952 kB\nMemFree:  100 kB\n"}
	cmds := map[string]string{"nvidia-smi --query-gpu=name,memory.total --format=csv,noheader,nounits": "NVIDIA GeForce RTX 4070, 12282\nNVIDIA T400, 4096\n"}
	h := DetectHardware(fakeProbe("linux", "amd64", cmds, files))
	if h.OS != "linux" || h.MemoryGB < 31 || h.MemoryGB > 32 {
		t.Fatalf("%+v", h)
	}
	if ok, vram := h.HasGPU("nvidia"); !ok || vram < 11.9 || vram > 12.1 {
		t.Fatalf("the largest GPU counts: %+v", h.GPUs)
	}
	// no nvidia-smi, no meminfo: unknowns stay zero
	h = DetectHardware(fakeProbe("linux", "arm64", nil, nil))
	if h.MemoryGB != 0 || len(h.GPUs) != 0 {
		t.Fatalf("%+v", h)
	}
}

func TestDetectWindows(t *testing.T) {
	cmds := map[string]string{"powershell -NoProfile -NonInteractive -Command (Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory": "17071996928\r\n"}
	h := DetectHardware(fakeProbe("windows", "amd64", cmds, nil))
	if h.OS != "windows" || h.MemoryGB < 15.8 || h.MemoryGB > 16 {
		t.Fatalf("%+v", h)
	}
}

func TestParsersToleratelGarbage(t *testing.T) {
	if parseMemInfo("nonsense") != 0 || len(parseNvidiaSmi("garbage\nx,y\n")) != 0 || parseUint("abc") != 0 {
		t.Fatal("garbage must parse to nothing")
	}
}

func TestRealProbeReadsThisMachine(t *testing.T) {
	h := DetectHardware(RealProbe(goosName(), archName(), t.TempDir()))
	if h.MemoryGB <= 0 || h.FreeDiskGB <= 0 || h.OS == "" {
		t.Fatalf("this machine has memory and disk: %+v", h)
	}
}

func goosName() string { return runtimeGOOS }
func archName() string { return runtimeGOARCH }
