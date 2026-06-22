package admin

// Role is an ordered privilege level on the admin API: viewer < operator < admin.
type Role int

const (
	RoleViewer Role = iota
	RoleOperator
	RoleAdmin
)

func (r Role) String() string {
	switch r {
	case RoleViewer:
		return "viewer"
	case RoleOperator:
		return "operator"
	case RoleAdmin:
		return "admin"
	default:
		return "unknown"
	}
}

// ParseRole maps a stored role-string to a Role. ok=false for anything else.
func ParseRole(s string) (Role, bool) {
	switch s {
	case "viewer":
		return RoleViewer, true
	case "operator":
		return RoleOperator, true
	case "admin":
		return RoleAdmin, true
	default:
		return 0, false
	}
}
