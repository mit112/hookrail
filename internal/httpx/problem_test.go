package httpx

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestProblemShape(t *testing.T) {
	w := httptest.NewRecorder()
	Problem(w, 422, "ssrf rejected", "url resolves to a blocked range")
	if got := w.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("content-type = %q", got)
	}
	if w.Code != 422 {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["title"] != "ssrf rejected" || body["status"].(float64) != 422 {
		t.Fatalf("body = %v", body)
	}
}

func TestClampLimit(t *testing.T) {
	cases := []struct{ in string; want int }{{"", 50}, {"10", 10}, {"500", 200}, {"-3", 50}, {"abc", 50}}
	for _, c := range cases {
		if got := ClampLimit(c.in, 50, 200); got != c.want {
			t.Fatalf("ClampLimit(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestCursorRoundTripAndReject(t *testing.T) {
	enc := EncodeCursor("01HZX...ulid")
	got, err := DecodeCursor(enc)
	if err != nil || got != "01HZX...ulid" {
		t.Fatalf("roundtrip got %q err %v", got, err)
	}
	if _, err := DecodeCursor("!!!not-base64!!!"); err == nil {
		t.Fatal("malformed cursor must error (handler maps to 400)")
	}
	if got, err := DecodeCursor(""); err != nil || got != "" {
		t.Fatalf("empty cursor must decode to empty start, got %q err %v", got, err)
	}
}
