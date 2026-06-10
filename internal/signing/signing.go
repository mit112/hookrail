// Package signing implements the Stripe-style webhook signature of spec §8:
//   hookrail-signature: t=<unix>,v1=hex(HMAC_SHA256(secret, t || '.' || delivery_id || '.' || body))
// Verify accepts multiple secrets to support the dual-secret rotation window.
package signing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const Header = "hookrail-signature"

var (
	ErrMalformedHeader         = errors.New("signing: malformed signature header")
	ErrTimestampOutOfTolerance = errors.New("signing: timestamp outside tolerance")
	ErrNoMatchingSignature     = errors.New("signing: no matching signature")
)

func mac(secret []byte, unix int64, deliveryID string, body []byte) []byte {
	m := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(m, "%d.%s.", unix, deliveryID) // errcheck: write to hash.Hash never fails
	m.Write(body)
	return m.Sum(nil)
}

func Sign(secret []byte, t time.Time, deliveryID string, body []byte) string {
	u := t.Unix()
	return fmt.Sprintf("t=%d,v1=%s", u, hex.EncodeToString(mac(secret, u, deliveryID, body)))
}

func Verify(secrets [][]byte, header, deliveryID string, body []byte, now time.Time, tolerance time.Duration) error {
	var unix int64 = -1
	var sigHex string
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			u, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return ErrMalformedHeader
			}
			unix = u
		case "v1":
			sigHex = v
		}
	}
	if unix < 0 || sigHex == "" {
		return ErrMalformedHeader
	}
	got, err := hex.DecodeString(sigHex)
	if err != nil || len(got) != sha256.Size {
		return ErrMalformedHeader
	}
	skew := now.Sub(time.Unix(unix, 0))
	if skew > tolerance || skew < -tolerance {
		return ErrTimestampOutOfTolerance
	}
	for _, s := range secrets {
		if hmac.Equal(got, mac(s, unix, deliveryID, body)) {
			return nil
		}
	}
	return ErrNoMatchingSignature
}
