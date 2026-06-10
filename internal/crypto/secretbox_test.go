package crypto

import (
	"bytes"
	"testing"
)

func key() [32]byte { var k [32]byte; copy(k[:], "0123456789abcdef0123456789abcdef"); return k }

func TestEncryptDecryptRoundTrip(t *testing.T) {
	k := key()
	pt := []byte("whsec_endpoint_secret")
	ct, err := Encrypt(k, pt)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, pt) {
		t.Fatal("ciphertext contains plaintext")
	}
	got, err := Decrypt(k, ct)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, pt) {
		t.Fatalf("round trip: got %q want %q", got, pt)
	}
}

func TestDecryptRejectsTamper(t *testing.T) {
	k := key()
	ct, _ := Encrypt(k, []byte("secret"))
	ct[len(ct)-1] ^= 0xff
	if _, err := Decrypt(k, ct); err == nil {
		t.Fatal("tampered ciphertext decrypted")
	}
}

func TestNoncesAreUnique(t *testing.T) {
	k := key()
	a, _ := Encrypt(k, []byte("x"))
	b, _ := Encrypt(k, []byte("x"))
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions identical — nonce reuse")
	}
}
