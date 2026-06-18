//go:build integration

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mit112/hookrail/internal/store"
)

func TestDeliveriesBrowseAndTimeline(t *testing.T) {
	srv, st := newServer(t)
	ctx := context.Background()
	keyID := seedPipeline(t, st, "dv.*")
	res, _ := st.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "dv.x", Payload: []byte(`{}`)})
	did := res.DeliveryIDs[0]

	// browse filtered by state=pending must return THIS delivery (not just 200)
	w := do(t, srv, "GET", "/v1/deliveries?state=pending", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("browse = %d", w.Code)
	}
	var page struct {
		Items []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	foundPending := false
	for _, it := range page.Items {
		if it.ID == did && it.State == "pending" {
			foundPending = true
		}
	}
	if !foundPending {
		t.Fatalf("state=pending filter did not return the pending delivery: %s", w.Body.String())
	}
	// negative filter: state=succeeded must NOT include it
	wn := do(t, srv, "GET", "/v1/deliveries?state=succeeded", nil)
	if strings.Contains(wn.Body.String(), did) {
		t.Fatal("state=succeeded filter wrongly returned a pending delivery")
	}

	// timeline shows the durable attempts_truncated marker (false here)
	tw := do(t, srv, "GET", "/v1/deliveries/"+did, nil)
	if tw.Code != http.StatusOK {
		t.Fatalf("timeline = %d", tw.Code)
	}
	var tl struct {
		State             string `json:"state"`
		AttemptsTruncated bool   `json:"attempts_truncated"`
	}
	_ = json.Unmarshal(tw.Body.Bytes(), &tl)
	if tl.State != "pending" || tl.AttemptsTruncated {
		t.Fatalf("timeline = %s", tw.Body.String())
	}

	// flip the durable marker and confirm it surfaces
	_, _ = st.Pool.Exec(ctx, `UPDATE deliveries SET attempts_truncated_at = now() WHERE id=$1`, did)
	tw2 := do(t, srv, "GET", "/v1/deliveries/"+did, nil)
	_ = json.Unmarshal(tw2.Body.Bytes(), &tl)
	if !tl.AttemptsTruncated {
		t.Fatal("attempts_truncated should be true after marker set")
	}
}
