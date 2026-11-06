//go:build windows

package authoringlock

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// The control directory's protection on Windows is its ACL, not mode bits
// (os.Chmod only toggles the read-only attribute there). A take must refuse a
// control directory whose DACL grants another principal access, and name the
// ACL as the reason. Red when ValidateOwnership stops auditing the ACEs.
func TestAControlDirectoryWithAnotherPrincipalIsRefused(t *testing.T) {
	path := authoringFile(t, "main.bot")
	first, err := Take(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	control := first.Identity().Control
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}

	sd, err := windows.GetNamedSecurityInfo(control, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	widened, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.FILE_GENERIC_WRITE,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(everyone),
		},
	}}, current)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(control, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, widened, nil); err != nil {
		t.Fatal(err)
	}

	l, err := Take(t.Context(), path)
	if err == nil {
		_ = l.Release()
		t.Fatal("the take accepted a control directory that grants Everyone write access")
	}
	if !strings.Contains(err.Error(), "authoring control ACL grants access to another principal") {
		t.Fatalf("the take answered %q, want it to name the ACL grant", err)
	}
}
