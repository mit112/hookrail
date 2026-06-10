package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func get(t *testing.T, ts *httptest.Server, path, deliveryID string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+path, nil)
	req.Header.Set("hookrail-delivery-id", deliveryID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close() //nolint:errcheck,gosec
	return resp.StatusCode
}

func TestSucceedMode(t *testing.T) {
	ts := httptest.NewServer(newReceiver().handler())
	defer ts.Close()
	if got := get(t, ts, "/succeed", "d1"); got != 200 {
		t.Fatalf("succeed = %d, want 200", got)
	}
}

func TestFailNTimesThenSucceed(t *testing.T) {
	ts := httptest.NewServer(newReceiver().handler())
	defer ts.Close()
	for i := 0; i < 2; i++ {
		if got := get(t, ts, "/fail/2", "d2"); got != 500 {
			t.Fatalf("attempt %d = %d, want 500", i+1, got)
		}
	}
	if got := get(t, ts, "/fail/2", "d2"); got != 200 {
		t.Fatalf("attempt 3 = %d, want 200 after 2 failures", got)
	}
	// independent counter per delivery id
	if got := get(t, ts, "/fail/2", "d3"); got != 500 {
		t.Fatalf("fresh delivery id = %d, want 500", got)
	}
}

func TestRedirectMode(t *testing.T) {
	ts := httptest.NewServer(newReceiver().handler())
	defer ts.Close()
	client := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/redirect", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close() //nolint:errcheck,gosec
	if resp.StatusCode != 302 {
		t.Fatalf("redirect = %d, want 302", resp.StatusCode)
	}
}

func TestReceivedLedger(t *testing.T) {
	ts := httptest.NewServer(newReceiver().handler())
	defer ts.Close()
	get(t, ts, "/succeed", "d9")
	get(t, ts, "/succeed", "d9")
	resp, err := http.Get(ts.URL + "/received?delivery_id=d9")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	var n int
	if _, err := fmt.Fscan(resp.Body, &n); err != nil || n != 2 {
		t.Fatalf("received count = %d (err %v), want 2", n, err)
	}
}
