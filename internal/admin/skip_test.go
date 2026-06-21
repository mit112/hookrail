//go:build integration

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mit112/hookrail/internal/admin"
	"github.com/mit112/hookrail/internal/store"
)

// doNoAuth issues a request against the admin handler WITHOUT any Authorization
// header, to test the 401 path.
func doNoAuth(t *testing.T, srv *admin.Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rdr)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	return w
}

// doWithToken issues a request against the admin handler with a specific
// Authorization: Bearer <token> value.
func doWithToken(t *testing.T, srv *admin.Server, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rdr)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	return w
}

func TestSkipEndpoint(t *testing.T) {
	srv, st := newServer(t)
	ctx := context.Background()

	keyID := seedPipeline(t, st, "orders.*")

	// Find the subscription ID and set ordered=true
	var subID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM subscriptions LIMIT 1`).Scan(&subID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE subscriptions SET ordered=true WHERE id=$1`, subID); err != nil {
		t.Fatal(err)
	}

	// Ingest 2 ordered events with the same ordering key
	ingest := func(seq int) string {
		res, err := st.IngestEvent(ctx, store.IngestParams{
			ProducerKeyID:       keyID,
			Topic:               "orders.x",
			Payload:             []byte(fmt.Sprintf(`{"seq":%d}`, seq)),
			OrderingKey:         "k1",
			OrderedKeyBacklogMax: 10000,
		})
		if err != nil {
			t.Fatalf("ingest seq %d: %v", seq, err)
		}
		if len(res.DeliveryIDs) != 1 {
			t.Fatalf("ingest seq %d: got %d deliveries, want 1", seq, len(res.DeliveryIDs))
		}
		return res.DeliveryIDs[0]
	}

	did1 := ingest(1) // ordering_seq=1, head
	did2 := ingest(2) // ordering_seq=2, non-head (pending)

	t.Run("no auth", func(t *testing.T) {
		w := doNoAuth(t, srv, "POST", "/v1/deliveries/"+did1+"/skip", nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("no auth = %d, want 401: %s", w.Code, w.Body.String())
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
			t.Fatalf("no auth content-type = %q, want application/problem+json", ct)
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		w := doWithToken(t, srv, "POST", "/v1/deliveries/"+did1+"/skip", nil, "wrong")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong token = %d, want 401: %s", w.Code, w.Body.String())
		}
	})

	t.Run("non-dead-lettered", func(t *testing.T) {
		// did1 is still pending (head but not dead_lettered)
		w := do(t, srv, "POST", "/v1/deliveries/"+did1+"/skip", nil)
		if w.Code != http.StatusConflict {
			t.Fatalf("non-dead-lettered = %d, want 409: %s", w.Code, w.Body.String())
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
			t.Fatalf("non-dead-lettered content-type = %q, want application/problem+json", ct)
		}
	})

	t.Run("unknown id", func(t *testing.T) {
		w := do(t, srv, "POST", "/v1/deliveries/nonexistent/skip", nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("unknown id = %d, want 404: %s", w.Code, w.Body.String())
		}
	})

	// Drive did1 to dead_lettered
	if _, err := st.Pool.Exec(ctx,
		`UPDATE deliveries SET state='dead_lettered', lease_until=NULL WHERE id=$1`, did1); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO dead_letters (delivery_id, final_error, endpoint_id)
		 SELECT $1, 'seeded', endpoint_id FROM deliveries WHERE id=$1`, did1); err != nil {
		t.Fatal(err)
	}

	t.Run("valid skip", func(t *testing.T) {
		w := do(t, srv, "POST", "/v1/deliveries/"+did1+"/skip", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("valid skip = %d, want 200: %s", w.Code, w.Body.String())
		}
		var body struct {
			DeliveryID string  `json:"delivery_id"`
			State      string  `json:"state"`
			NextHead   *string `json:"next_head_delivery_id"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("valid skip unmarshal: %v", err)
		}
		if body.DeliveryID != did1 {
			t.Fatalf("delivery_id = %q, want %q", body.DeliveryID, did1)
		}
		if body.State != "skipped" {
			t.Fatalf("state = %q, want skipped", body.State)
		}
		if body.NextHead == nil {
			t.Fatal("next_head_delivery_id is nil, want non-nil (did2)")
		}
		if *body.NextHead != did2 {
			t.Fatalf("next_head_delivery_id = %q, want %q", *body.NextHead, did2)
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		w := do(t, srv, "POST", "/v1/deliveries/"+did1+"/skip", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("idempotent skip = %d, want 200: %s", w.Code, w.Body.String())
		}
		var body struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("idempotent unmarshal: %v", err)
		}
		if body.State != "skipped" {
			t.Fatalf("state = %q, want skipped", body.State)
		}
	})
}
