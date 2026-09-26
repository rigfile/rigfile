//go:build windows

package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WritePrivate atomically writes data to path so that only the current user (plus SYSTEM and Administrators,
// who can take ownership anyway) can read it: the temp file is created empty, its DACL is replaced by a
// protected (non-inheriting) user-only ACL, and only then is the secret written and the file renamed into
// place. The data is therefore never on disk under a broader ACL (plan §7.3 rule 5).
func WritePrivate(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".rigfile-tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if err := restrictToUser(name); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("restrict %s to the current user: %w", filepath.Base(path), err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	// os.Rename is MoveFileEx with MOVEFILE_REPLACE_EXISTING: atomic on the same volume. The ACL travels with the file.
	if err := RenameReplace(name, path); err != nil {
		cleanup()
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}

func currentUserSID() (*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return u.User.Sid, nil
}

func restrictToUser(path string) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}, nil)
	if err != nil {
		return err
	}
	// PROTECTED: do not inherit the parent directory's ACEs (that is where "Users: read" usually comes from).
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

// IsPrivateFile returns nil only if path exists and its DACL grants access to nobody but the current user,
// SYSTEM and Administrators. A missing (NULL) DACL means "everyone" and is refused.
func IsPrivateFile(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if dacl == nil {
		return fmt.Errorf("%s has no access control list (everyone can read it); recreate it with `rigfile secrets set`", path)
	}
	self, err := currentUserSID()
	if err != nil {
		return err
	}
	var allowed []string
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		allowed = append(allowed, (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String())
	}
	if bad := ForeignTrustees(allowed, self.String()); len(bad) > 0 {
		return fmt.Errorf("%s is accessible by other accounts (%s); fix with: icacls \"%s\" /inheritance:r /grant:r \"%%USERNAME%%:F\"",
			path, strings.Join(bad, ", "), path)
	}
	return nil
}

// RestrictToUser replaces the file's ACL with a protected, user-only one.
func RestrictToUser(path string) error { return restrictToUser(path) }
