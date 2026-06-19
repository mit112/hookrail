//go:build integration

package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mit112/hookrail/internal/store"
)

func TestGetEventStatus_MissingEvent_ReturnsErrNotFound(t *testing.T) {
	st := testStore(t) // existing testcontainers helper
	_, err := st.GetEventStatus(context.Background(), "01J000000000000000MISSING0")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want store.ErrNotFound, got %v", err)
	}
}
