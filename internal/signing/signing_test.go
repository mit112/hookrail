package signing

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var (
	secret     = []byte("whsec_test_secret_1")
	oldSecret  = []byte("whsec_test_secret_0")
	body       = []byte(`{"order_id":"o_123","amount":4200}`)
	now        = time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	tol        = 5 * time.Minute
	deliveryID = "01JXAMPLEDELIVERYID0000000"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	h := Sign(secret, now, deliveryID, body)
	if !strings.HasPrefix(h, "t=1781092800,v1=") {
		t.Fatalf("unexpected header shape: %s", h)
	}
	if err := Verify([][]byte{secret}, h, deliveryID, body, now, tol); err != nil {
		t.Fatalf("Verify failed on valid signature: %v", err)
	}
}

func TestVerifyRejectsTamperedBody(t *testing.T) {
	h := Sign(secret, now, deliveryID, body)
	bad := []byte(`{"order_id":"o_123","amount":9999}`)
	if err := Verify([][]byte{secret}, h, deliveryID, bad, now, tol); !errors.Is(err, ErrNoMatchingSignature) {
		t.Fatalf("want ErrNoMatchingSignature, got %v", err)
	}
}

func TestVerifyRejectsWrongDeliveryID(t *testing.T) {
	h := Sign(secret, now, deliveryID, body)
	if err := Verify([][]byte{secret}, h, "01JOTHERDELIVERY0000000000", body, now, tol); !errors.Is(err, ErrNoMatchingSignature) {
		t.Fatalf("want ErrNoMatchingSignature, got %v", err)
	}
}

func TestVerifyTimestampTolerance(t *testing.T) {
	h := Sign(secret, now, deliveryID, body)
	for _, skew := range []time.Duration{6 * time.Minute, -6 * time.Minute} {
		if err := Verify([][]byte{secret}, h, deliveryID, body, now.Add(skew), tol); !errors.Is(err, ErrTimestampOutOfTolerance) {
			t.Fatalf("skew %v: want ErrTimestampOutOfTolerance, got %v", skew, err)
		}
	}
	// exactly at the boundary is accepted
	if err := Verify([][]byte{secret}, h, deliveryID, body, now.Add(tol), tol); err != nil {
		t.Fatalf("boundary skew rejected: %v", err)
	}
}

func TestVerifyDualSecretRotation(t *testing.T) {
	// signed with the old secret; receiver holds [new, old] during rotation (§8)
	h := Sign(oldSecret, now, deliveryID, body)
	if err := Verify([][]byte{secret, oldSecret}, h, deliveryID, body, now, tol); err != nil {
		t.Fatalf("rotation window verify failed: %v", err)
	}
	if err := Verify([][]byte{secret}, h, deliveryID, body, now, tol); !errors.Is(err, ErrNoMatchingSignature) {
		t.Fatalf("want ErrNoMatchingSignature after rotation completes, got %v", err)
	}
}

func TestVerifyMalformedHeaders(t *testing.T) {
	for _, h := range []string{"", "v1=abc", "t=123", "t=abc,v1=00", "t=123,v2=00", "t=123,v1=zz"} {
		if err := Verify([][]byte{secret}, h, deliveryID, body, now, tol); !errors.Is(err, ErrMalformedHeader) {
			t.Errorf("header %q: want ErrMalformedHeader, got %v", h, err)
		}
	}
}
