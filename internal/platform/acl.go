package platform

import "sort"

// Windows has no mode bits: "private" means the file's DACL grants access to the owner and to the two
// principals that can always override it anyway (SYSTEM and the Administrators group), and to nobody else.
// The decision is a pure function of the SIDs found in the DACL, so it is unit-tested on every host; the
// Windows-only code (perm_windows.go) reads and writes the real ACL.
const (
	sidSystem         = "S-1-5-18"     // NT AUTHORITY\SYSTEM
	sidAdministrators = "S-1-5-32-544" // BUILTIN\Administrators
)

// ForeignTrustees returns the SIDs (sorted, de-duplicated) among allowed that may not appear in a private
// file's DACL: everyone except self, SYSTEM and Administrators. Well-known "everyone" SIDs (S-1-1-0 World,
// S-1-5-11 Authenticated Users, S-1-5-32-545 Users) are therefore reported.
func ForeignTrustees(allowed []string, self string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range allowed {
		if s == self || s == sidSystem || s == sidAdministrators || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
