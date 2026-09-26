package claudecode

import (
	"strings"
	"testing"

	basesecure "github.com/digitaldreamer3462/rigfile/base-secure"
	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
)

// base-secure has PowerShell forms of its shell rules and Windows credential locations; they apply on Windows
// only, and macOS/Linux never see a PowerShell rule.
func TestBaseSecurePowerShellRulesAreWindowsOnly(t *testing.T) {
	files, err := basesecure.Files()
	if err != nil {
		t.Fatal(err)
	}
	perms, err := manifest.ParsePermissions(files["rigfile.yaml"])
	if err != nil {
		t.Fatal(err)
	}
	render := func(goos string) string {
		pi, _ := platform.New(platform.Options{GOOS: goos, Getenv: func(k string) string {
			if k == "USERPROFILE" || k == "HOME" {
				return "/h"
			}
			return ""
		}})
		p, err := PlanPermissions(pi, []byte("{}\n"), perms)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range p.Adds {
			out = append(out, c.List+" "+c.Rule)
		}
		return strings.Join(out, "\n")
	}
	win, mac := render("windows"), render("darwin")
	for _, want := range []string{"deny PowerShell(git*--no-verify*)", "deny PowerShell(Get-ChildItem env:*)",
		"ask PowerShell(Remove-Item*-Recurse*)", "deny Read(~/AppData/Roaming/Microsoft/Protect/**)", "deny Read(~/.ssh/**)"} {
		if !strings.Contains(win, want) {
			t.Fatalf("windows is missing %q", want)
		}
	}
	if strings.Contains(win, "/etc/shadow") || strings.Contains(win, "Library/Keychains") {
		t.Fatal("macOS/Linux paths leaked onto Windows")
	}
	if strings.Contains(mac, "PowerShell(") || strings.Contains(mac, "AppData") {
		t.Fatalf("Windows rules leaked onto macOS:\n%s", mac)
	}
}

func TestWSLAddsWindowsSideDeniesOnlyInsideWSLAndOnlyWithBaseSecure(t *testing.T) {
	mk := func(env map[string]string) *platform.Info {
		pi, _ := platform.New(platform.Options{GOOS: "linux", Getenv: func(k string) string { return env[k] }})
		return pi
	}
	wsl := mk(map[string]string{"HOME": "/home/me", "WSL_DISTRO_NAME": "Ubuntu"})
	if !wsl.WSL || !mk(map[string]string{"HOME": "/home/me", "WSL_INTEROP": "/run/WSL/1_interop"}).WSL {
		t.Fatal("WSL not detected")
	}
	if mk(map[string]string{"HOME": "/home/me"}).WSL || len(WSLDenies(mk(map[string]string{"HOME": "/h"}))) != 0 {
		t.Fatal("plain Linux must not get Windows-side rules")
	}
	p, err := PlanPermissions(wsl, []byte("{}\n"), manifest.Permissions{Deny: WSLDenies(wsl)})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range p.Adds {
		got = append(got, c.Rule)
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{"Read(//mnt/c/Users/*/.ssh/**)", "Read(//mnt/c/Users/*/AppData/Roaming/Microsoft/Protect/**)"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in\n%s", want, joined)
		}
	}
}
