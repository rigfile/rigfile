package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/models"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
)

const modelsUsage = `usage: rigfile models <command>

  list                       the catalog's roles and what would be chosen on this machine
`

// detectHardware reads this machine (or the injected hardware in tests).
func detectHardware(e env) (platform.Hardware, error) {
	if e.hardware != nil {
		return *e.hardware, nil
	}
	pi, err := platformInfo(e)
	if err != nil {
		return platform.Hardware{}, err
	}
	home, err := pi.Home()
	if err != nil {
		return platform.Hardware{}, err
	}
	return platform.DetectHardware(platform.RealProbe(runtime.GOOS, runtime.GOARCH, filepath.Join(home, ".cache"))), nil
}

func cmdModels(args []string, e env) int {
	if len(args) == 0 {
		fmt.Fprint(e.err, modelsUsage)
		return 2
	}
	switch args[0] {
	case "list":
		return modelsList(e)
	}
	fmt.Fprint(e.err, modelsUsage)
	return 2
}

func modelsList(e env) int {
	cat, err := models.LoadCatalog()
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	hw, err := detectHardware(e)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	fmt.Fprintf(e.out, "This machine: %s/%s, %.0f GB memory, %.0f GB free disk", hw.OS, hw.Arch, hw.MemoryGB, hw.FreeDiskGB)
	for _, g := range hw.GPUs {
		fmt.Fprintf(e.out, ", GPU %s (%.0f GB)", g.Name, g.VRAMGB)
	}
	fmt.Fprintf(e.out, "\nCatalog checked %s\n", cat.Checked)
	names := make([]string, 0, len(cat.Roles))
	for n := range cat.Roles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		r := cat.Roles[n]
		fmt.Fprintf(e.out, "\n%s: %s\n", n, r.Description)
		p := models.Resolve(n, manifest.Model{Role: n}, cat, hw)
		p.Render(e.out)
	}
	return 0
}
