//go:build integration

package admin_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// validateOpResponse does SCHEMA-LEVEL conformance directly against the
// operation's response schema (design §6). It deliberately does NOT use
// gorillamux: that router consumes document/path-item servers, NOT
// Operation.Servers, so it cannot prove the per-op :8082 override. Instead we
// assert the per-op server explicitly AND validate the body via Schema.VisitJSON.
func validateOpResponse(t *testing.T, doc *openapi3.T, method, path string, status int, body []byte) {
	t.Helper()
	pi := doc.Paths.Value(path)
	if pi == nil {
		t.Fatalf("path %s not in spec", path)
	}
	op := pi.GetOperation(method)
	if op == nil {
		t.Fatalf("operation %s %s not in spec", method, path)
	}
	if op.Servers == nil || len(*op.Servers) == 0 || (*op.Servers)[0].URL != "http://localhost:8082" {
		t.Fatalf("%s %s must declare per-op server http://localhost:8082 (design §6)", method, path)
	}
	resp := op.Responses.Status(status)
	if resp == nil || resp.Value == nil {
		t.Fatalf("no %d response defined for %s %s", status, method, path)
	}
	mt := resp.Value.Content.Get("application/json")
	if mt == nil || mt.Schema == nil || mt.Schema.Value == nil {
		t.Fatalf("no application/json schema for %s %s %d", method, path, status)
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if err := mt.Schema.Value.VisitJSON(v); err != nil {
		t.Fatalf("response does not conform (%s %s %d): %v", method, path, status, err)
	}
}

func TestAdminResponsesMatchOpenAPI(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("spec invalid: %v", err)
	}
	srv, _ := newServer(t)

	// create-endpoint 201 conforms AND carries the per-op :8082 server
	ew := do(t, srv, "POST", "/v1/endpoints", map[string]string{"url": "https://example.com/h"})
	if ew.Code != 201 {
		t.Fatalf("create = %d body=%s", ew.Code, ew.Body.String())
	}
	validateOpResponse(t, doc, "POST", "/v1/endpoints", 201, ew.Body.Bytes())

	// list 200 conforms (the {items, next_cursor} envelope)
	lw := do(t, srv, "GET", "/v1/endpoints", nil)
	if lw.Code != 200 {
		t.Fatalf("list = %d", lw.Code)
	}
	validateOpResponse(t, doc, "GET", "/v1/endpoints", 200, lw.Body.Bytes())

	// NEGATIVE CONTROL: a body missing the required `secret`/`url` must FAIL the
	// create-201 schema — proving the schema is not vacuously permissive.
	pi := doc.Paths.Value("/v1/endpoints")
	schema := pi.GetOperation("POST").Responses.Status(201).Value.Content.Get("application/json").Schema.Value
	if err := schema.VisitJSON(map[string]any{"id": "x"}); err == nil {
		t.Fatal("create-201 schema accepted a body missing required url/secret — schema is vacuous")
	}
}
