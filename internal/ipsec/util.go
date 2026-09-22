package ipsec

import (
	"crypto/rand"
	"crypto/subtle"
)

func randRead(b []byte) (int, error) {
	return rand.Read(b)
}

func constantTimeEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
