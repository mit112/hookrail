//go:build integration

package admin_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestEndpointCreateGetListSoftDelete(t *testing.T) {
	srv, _ := newServer(t)

	// create — secret returned once, SSRF-validated
	w := do(t, srv, "POST", "/v1/endpoints", map[string]string{"url": "https://example.com/hook", "description": "d"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d body=%s", w.Code, w.Body.String())
	}
	var created struct{ ID, Secret string }
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.ID == "" || !strings.HasPrefix(created.Secret, "whsec_") {
		t.Fatalf("create body = %s", w.Body.String())
	}

	// get
	if g := do(t, srv, "GET", "/v1/endpoints/"+created.ID, nil); g.Code != http.StatusOK {
		t.Fatalf("get = %d", g.Code)
	}

	// soft-delete then excluded from list
	if d := do(t, srv, "DELETE", "/v1/endpoints/"+created.ID, nil); d.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", d.Code)
	}
	l := do(t, srv, "GET", "/v1/endpoints", nil)
	if strings.Contains(l.Body.String(), created.ID) {
		t.Fatal("soft-deleted endpoint still listed without include_deleted")
	}
	li := do(t, srv, "GET", "/v1/endpoints?include_deleted=true", nil)
	if !strings.Contains(li.Body.String(), created.ID) {
		t.Fatal("include_deleted=true should surface the soft-deleted endpoint")
	}
}

func TestEndpointCreateRejectsSSRF(t *testing.T) {
	srv, _ := newServer(t)
	w := do(t, srv, "POST", "/v1/endpoints", map[string]string{"url": "http://169.254.169.254/latest"})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("SSRF create = %d, want 422", w.Code)
	}
}

func TestEndpointPatchPartial(t *testing.T) {
	srv, _ := newServer(t)
	cw := do(t, srv, "POST", "/v1/endpoints", map[string]string{"url": "https://example.com/a", "description": "orig"})
	var ep struct{ ID string }
	_ = json.Unmarshal(cw.Body.Bytes(), &ep)

	// description-only PATCH must NOT require/clobber the URL
	if p := do(t, srv, "PATCH", "/v1/endpoints/"+ep.ID, map[string]string{"description": "updated"}); p.Code != http.StatusNoContent {
		t.Fatalf("description-only PATCH = %d, want 204", p.Code)
	}
	g := do(t, srv, "GET", "/v1/endpoints/"+ep.ID, nil)
	var got struct{ URL, Description string }
	_ = json.Unmarshal(g.Body.Bytes(), &got)
	if got.URL != "https://example.com/a" || got.Description != "updated" {
		t.Fatalf("after description PATCH: url=%q desc=%q (url must be unchanged)", got.URL, got.Description)
	}
	// url-only PATCH re-validates SSRF and updates only the url
	if p := do(t, srv, "PATCH", "/v1/endpoints/"+ep.ID, map[string]string{"url": "https://example.com/b"}); p.Code != http.StatusNoContent {
		t.Fatalf("url-only PATCH = %d, want 204", p.Code)
	}
	if bad := do(t, srv, "PATCH", "/v1/endpoints/"+ep.ID, map[string]string{"url": "http://169.254.169.254/x"}); bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("SSRF url PATCH = %d, want 422", bad.Code)
	}
}
