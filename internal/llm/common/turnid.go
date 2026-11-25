package common

import (
	"crypto/rand"
	"encoding/hex"
)

// RandomTurnID returns a random hex id suitable for conversation turn correlation.
func RandomTurnID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
