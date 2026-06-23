// Command hookrail-dash-hash generates argon2id PHC entries for the dashboard
// users file: prints `username:hash:role` (or the hash alone with --hash-only).
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mit112/hookrail/internal/admin"
	"github.com/mit112/hookrail/internal/dashboard"
)

func emitLine(user, hash, role string) string {
	return user + ":" + hash + ":" + role
}

func main() {
	var user, role, pw string
	var hashOnly bool
	flag.StringVar(&user, "user", "", "username")
	flag.StringVar(&role, "role", "viewer", "role: viewer|operator|admin")
	flag.StringVar(&pw, "password", "", "password (omit to read from stdin)")
	flag.BoolVar(&hashOnly, "hash-only", false, "print only the PHC hash")
	flag.Parse()

	if !hashOnly {
		if user == "" {
			fmt.Fprintln(os.Stderr, "error: --user required (or use --hash-only)")
			os.Exit(2)
		}
		if _, ok := admin.ParseRole(role); !ok {
			fmt.Fprintln(os.Stderr, "error: --role must be viewer|operator|admin")
			os.Exit(2)
		}
	}
	if pw == "" {
		fmt.Fprint(os.Stderr, "password: ")
		sc := bufio.NewScanner(os.Stdin)
		if sc.Scan() {
			pw = strings.TrimRight(sc.Text(), "\r\n")
		}
	}
	if pw == "" {
		fmt.Fprintln(os.Stderr, "error: empty password")
		os.Exit(2)
	}
	h, err := dashboard.HashPassword(pw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if hashOnly {
		fmt.Println(h)
		return
	}
	fmt.Println(emitLine(user, h, role))
}
