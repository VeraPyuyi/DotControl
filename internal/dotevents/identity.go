package dotevents

import (
	"crypto/sha256"
	"encoding/hex"
)

// The occurrence eventId identifies the source notice. Standard Webhooks IDs
// identify a delivery to one subscription and remain stable across retries.
func deliveryID(subscriptionID, eventID string) string {
	sum := sha256.Sum256([]byte(deliveryKey(subscriptionID, eventID)))
	return "evt_" + hex.EncodeToString(sum[:])
}
