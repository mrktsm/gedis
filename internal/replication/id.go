package replication

import (
	"crypto/rand"
	"encoding/hex"
	"io"
)

const replicationIDBytes = 20

func NewID() (string, error) {
	return newID(rand.Reader)
}

func newID(random io.Reader) (string, error) {
	data := make([]byte, replicationIDBytes)
	if _, err := io.ReadFull(random, data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}
