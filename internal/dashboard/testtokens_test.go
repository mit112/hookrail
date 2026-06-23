package dashboard

import "strings"

// Valid minted-format role tokens (hkadm_ + 48 hex) for tests across the package.
var (
	tokViewer   = "hkadm_" + strings.Repeat("a", 48)
	tokOperator = "hkadm_" + strings.Repeat("b", 48)
	tokAdmin    = "hkadm_" + strings.Repeat("c", 48)
)

// goodRoleTokensFile returns a valid three-role tokens file body.
func goodRoleTokensFile() string {
	return "viewer:" + tokViewer + "\noperator:" + tokOperator + "\nadmin:" + tokAdmin + "\n"
}
