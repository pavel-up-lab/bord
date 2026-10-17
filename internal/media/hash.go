package media

import "crypto/sha256"

func sha256Bytes(data []byte) [32]byte { return sha256.Sum256(data) }
