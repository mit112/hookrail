//go:build !ignore

package admin

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestOpenAPIParamsMatchServer(t *testing.T) {
	doc := loadSpec(t)
	q := func(path, method, name string) bool {
		op := doc.Paths.Find(path).GetOperation(method)
		for _, p := range op.Parameters {
			if p.Value != nil && p.Value.Name == name && p.Value.In == "query" {
				return true
			}
		}
		return false
	}
	if !q("/v1/deliveries", "GET", "topic") {
		t.Error("deliveries GET missing query param topic")
	}
	if q("/v1/deliveries", "GET", "topic_pattern") {
		t.Error("deliveries GET should NOT document topic_pattern")
	}
	if !q("/v1/endpoints", "GET", "include_deleted") {
		t.Error("endpoints list GET missing include_deleted")
	}
	if !q("/v1/endpoints/{id}", "GET", "include_deleted") {
		t.Error("endpoint GET missing include_deleted")
	}
	if !q("/v1/subscriptions/{id}", "GET", "include_deleted") {
		t.Error("subscription GET missing include_deleted")
	}
	// subscriptions LIST does NOT support include_deleted (store hardcodes deleted_at IS NULL)
	if q("/v1/subscriptions", "GET", "include_deleted") {
		t.Error("subscriptions list must NOT document include_deleted")
	}
	if !q("/v1/dlq", "GET", "replayed") {
		t.Error("dlq GET missing replayed")
	}
	if !q("/v1/dlq", "GET", "until") {
		t.Error("dlq GET missing until")
	}
}

func TestOpenAPIDocumentsNoStoreHeader(t *testing.T) {
	doc := loadSpec(t)
	has := func(path, method, code string) bool {
		op := doc.Paths.Find(path).GetOperation(method)
		resp := op.Responses.Status(mustAtoi(code))
		return resp != nil && resp.Value != nil && resp.Value.Headers["Cache-Control"] != nil
	}
	if !has("/v1/endpoints", "POST", "201") {
		t.Error("create-endpoint 201 missing Cache-Control header (secret in body)")
	}
	if !has("/v1/endpoints/{id}/rotate-secret", "POST", "200") {
		t.Error("rotate-secret 200 missing Cache-Control header")
	}
}

func mustAtoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}
