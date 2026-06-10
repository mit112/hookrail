// Package crypto encrypts endpoint HMAC secrets at rest with AES-256-GCM
// (spec §4: encrypted not hashed — signing needs the plaintext back).
// Output layout: nonce(12) || ciphertext+tag.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

func gcm(key [32]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func Encrypt(key [32]byte, plaintext []byte) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, plaintext, nil), nil
}

func Decrypt(key [32]byte, box []byte) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	if len(box) < g.NonceSize() {
		return nil, errors.New("crypto: ciphertext too short")
	}
	return g.Open(nil, box[:g.NonceSize()], box[g.NonceSize():], nil)
}
