package dotqueue

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"io"
)

const envelopeVersion = 1

func seal(key []byte, plaintext []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, errf("unknown")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errf("unknown")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errf("unknown")
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, errf("unknown")
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, []byte{'D', envelopeVersion})
	out := make([]byte, 2+len(nonce)+len(ciphertext))
	out[0] = 'D'
	out[1] = envelopeVersion
	copy(out[2:], nonce)
	copy(out[2+len(nonce):], ciphertext)
	return out, nil
}

func openEnvelope(key []byte, data []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, errf("corrupt")
	}
	if len(data) < 3 || data[0] != 'D' || data[1] != envelopeVersion {
		return nil, errf("corrupt")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errf("corrupt")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errf("corrupt")
	}
	if len(data) < 2+gcm.NonceSize() {
		return nil, errf("corrupt")
	}
	nonce := data[2 : 2+gcm.NonceSize()]
	ciphertext := data[2+gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, data[:2])
	if err != nil {
		return nil, errf("corrupt")
	}
	return plain, nil
}
