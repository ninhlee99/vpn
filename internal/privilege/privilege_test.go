package privilege

import "testing"

func TestOwnerCheckSkippedWithoutSetuidPrivilege(t *testing.T) {
	// A release candidate is executed as this ordinary process for `vpn
	// version` before install. It must not need access to a root-only owner
	// file that an existing install may have created.
	if err := CheckOwner(); err != nil {
		t.Fatalf("CheckOwner() = %v, want nil for ordinary process", err)
	}
}
