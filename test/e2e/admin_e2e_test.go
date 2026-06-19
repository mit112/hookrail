//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"
)

var (
	adminURL   = env("E2E_ADMIN_URL", "http://localhost:8082")
	adminToken = env("E2E_ADMIN_TOKEN", "dev-admin-token-001")
)

func adminReq(t *testing.T, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, adminURL+path, rdr)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("admin %s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	return resp, buf.Bytes()
}

func TestAdminHappyPath(t *testing.T) {
	if os.Getenv("E2E_ADMIN_TOKEN") == "" && adminToken == "" {
		t.Skip("admin token unset")
	}
	// create endpoint pointing at the in-stack receiver
	resp, b := adminReq(t, "POST", "/v1/endpoints", map[string]string{"url": "http://test-receiver:9090/succeed"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create endpoint = %d: %s", resp.StatusCode, b)
	}
	var ep struct{ ID string }
	_ = json.Unmarshal(b, &ep)

	// create subscription
	resp, b = adminReq(t, "POST", "/v1/subscriptions", map[string]any{"topic_pattern": "e2eadmin.*", "endpoint_id": ep.ID, "max_attempts": 5})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create sub = %d: %s", resp.StatusCode, b)
	}

	// ingest via the PUBLIC api (postEvent uses E2E_PRODUCER_KEY from make seed)
	got := postEvent(t, "e2eadmin.created", `{"hello":"admin"}`)
	if len(got.DeliveryIDs) == 0 {
		t.Fatal("no deliveries created — is the subscription active and the seeded key valid?")
	}

	// poll admin deliveries until succeeded
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		_, b = adminReq(t, "GET", "/v1/deliveries/"+got.DeliveryIDs[0], nil)
		var tl struct{ State string `json:"state"` }
		_ = json.Unmarshal(b, &tl)
		if tl.State == "succeeded" {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("delivery did not reach succeeded within 30s")
}
