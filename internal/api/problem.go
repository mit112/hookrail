package api

import (
	"net/http"

	"github.com/mit112/hookrail/internal/httpx"
)

// problem writes an RFC 7807 problem+json response (§9). Thin alias over
// httpx.Problem so api and admin share one implementation (design §1).
func problem(w http.ResponseWriter, status int, title, detail string) {
	httpx.Problem(w, status, title, detail)
}
