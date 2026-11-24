package files

import (
	"crypto/rand"
	"encoding/hex"
)

func randomName() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}
