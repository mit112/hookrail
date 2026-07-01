package dashboard

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mit112/hookrail/internal/admin"
)

type Config struct {
	SessionKey     []byte
	SessionPrev    []byte // optional; nil when unset
	ProducerKey    string
	AdminURL       string
	IngressURL     string
	Addr           string
	SessionTTL     time.Duration
	InsecureCookie bool

	// RBAC R2: per-user accounts + role-matched upstream tokens, loaded from
	// mounted secret files.
	UsersFile      string
	RoleTokensFile string
	Users          *Users
	RoleTokens     *RoleTokens
	AttestInterval time.Duration

	// Public read-only demo. When DemoMode is set, the SPA auto-authenticates
	// visitors as DemoUser via POST /api/demo-login. DemoUser MUST resolve to
	// the viewer role in the users file — validated fail-closed at load, so the
	// demo can never hand a public visitor anything above read-only.
	DemoMode bool
	DemoUser string
}

func LoadConfig() (Config, error) {
	var c Config
	req := func(k string) (string, error) {
		v := os.Getenv(k)
		if strings.TrimSpace(v) == "" {
			return "", fmt.Errorf("required env %s is unset", k)
		}
		return v, nil
	}
	var err error
	sk := os.Getenv("HOOKRAIL_DASHBOARD_SESSION_KEY")
	if len(sk) < 32 { return c, errors.New("HOOKRAIL_DASHBOARD_SESSION_KEY must be >= 32 bytes") }
	c.SessionKey = []byte(sk)
	if p := os.Getenv("HOOKRAIL_DASHBOARD_SESSION_KEY_PREVIOUS"); p != "" {
		if len(p) < 32 { return c, errors.New("HOOKRAIL_DASHBOARD_SESSION_KEY_PREVIOUS must be >= 32 bytes") }
		c.SessionPrev = []byte(p)
	}
	keyFile, err := req("HOOKRAIL_PRODUCER_KEY_FILE")
	if err != nil { return c, err }
	// Path from env var, by design.
	b, err := os.ReadFile(keyFile) //nolint:gosec
	if err != nil { return c, fmt.Errorf("reading HOOKRAIL_PRODUCER_KEY_FILE: %w", err) }
	c.ProducerKey = strings.TrimSpace(string(b))
	if !strings.HasPrefix(c.ProducerKey, "hk_") { return c, errors.New("producer key file must contain an hk_ key") }
	if c.AdminURL, err = req("HOOKRAIL_ADMIN_URL"); err != nil { return c, err }
	if c.IngressURL, err = req("HOOKRAIL_INGRESS_URL"); err != nil { return c, err }
	for _, u := range []string{c.AdminURL, c.IngressURL} {
		pu, perr := url.Parse(u)
		if perr != nil || (pu.Scheme != "http" && pu.Scheme != "https") {
			return c, fmt.Errorf("invalid upstream URL %q", u)
		}
	}
	c.Addr = os.Getenv("HOOKRAIL_DASHBOARD_ADDR")
	if c.Addr == "" { c.Addr = ":8085" }
	c.SessionTTL = 12 * time.Hour
	if v := os.Getenv("HOOKRAIL_DASHBOARD_SESSION_TTL"); v != "" {
		if d, derr := time.ParseDuration(v); derr == nil { c.SessionTTL = d } else { return c, fmt.Errorf("bad HOOKRAIL_DASHBOARD_SESSION_TTL: %w", derr) }
	}
	c.InsecureCookie = os.Getenv("HOOKRAIL_DASHBOARD_INSECURE_COOKIE") == "true"

	// RBAC R2 user + role-token files (fail closed at load).
	if c.UsersFile, err = req("HOOKRAIL_DASHBOARD_USERS_FILE"); err != nil {
		return c, err
	}
	if c.Users, err = LoadUsers(c.UsersFile); err != nil {
		return c, err
	}
	if c.RoleTokensFile, err = req("HOOKRAIL_DASHBOARD_ROLE_TOKENS_FILE"); err != nil {
		return c, err
	}
	if c.RoleTokens, err = LoadRoleTokens(c.RoleTokensFile); err != nil {
		return c, err
	}
	c.AttestInterval = 60 * time.Second
	if v := os.Getenv("HOOKRAIL_DASHBOARD_ATTEST_INTERVAL"); v != "" {
		d, derr := time.ParseDuration(v)
		if derr != nil {
			return c, fmt.Errorf("bad HOOKRAIL_DASHBOARD_ATTEST_INTERVAL: %w", derr)
		}
		if d <= 0 {
			return c, fmt.Errorf("HOOKRAIL_DASHBOARD_ATTEST_INTERVAL must be > 0")
		}
		c.AttestInterval = d
	}

	// Public demo mode: fail closed. If enabled, the configured demo user must
	// exist and be viewer — never anything with write/destructive privileges.
	c.DemoMode = os.Getenv("HOOKRAIL_DASHBOARD_DEMO_MODE") == "true"
	if c.DemoMode {
		c.DemoUser = strings.TrimSpace(os.Getenv("HOOKRAIL_DASHBOARD_DEMO_USER"))
		if c.DemoUser == "" {
			c.DemoUser = "demo"
		}
		role, ok := c.Users.RoleOf(c.DemoUser)
		if !ok {
			return c, fmt.Errorf("HOOKRAIL_DASHBOARD_DEMO_MODE is set but demo user %q is not in the users file", c.DemoUser)
		}
		if role != admin.RoleViewer {
			return c, fmt.Errorf("demo user %q must have role viewer, has %q", c.DemoUser, role.String())
		}
	}
	return c, nil
}
