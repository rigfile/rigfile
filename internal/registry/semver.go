package registry

import (
	"strconv"
	"strings"
)

// version is a parsed x.y.z[-pre].
type version struct {
	n   [3]int
	pre string
}

func parseVersion(s string) (version, bool) {
	var v version
	core, pre, _ := strings.Cut(s, "-")
	v.pre = pre
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v.n[i] = n
	}
	return v, true
}

// less orders versions: numerically, and a pre-release before its release.
func (a version) less(b version) bool {
	for i := 0; i < 3; i++ {
		if a.n[i] != b.n[i] {
			return a.n[i] < b.n[i]
		}
	}
	switch {
	case a.pre == b.pre:
		return false
	case a.pre != "" && b.pre == "":
		return true
	case a.pre == "" && b.pre != "":
		return false
	}
	return a.pre < b.pre
}

// CompareVersions returns -1, 0 or 1.
func CompareVersions(a, b string) int {
	va, _ := parseVersion(a)
	vb, _ := parseVersion(b)
	switch {
	case va.less(vb):
		return -1
	case vb.less(va):
		return 1
	}
	return 0
}
