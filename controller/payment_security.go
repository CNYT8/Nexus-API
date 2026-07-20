package controller

import (
	"crypto/sha256"
	"encoding/hex"
)

// paymentPayloadFingerprint keeps webhook logs useful for correlation without
// persisting customer data, signatures, or replayable callback bodies.
func paymentPayloadFingerprint(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
