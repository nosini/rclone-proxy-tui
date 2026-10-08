package state

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"math/big"
)

// Password alphabet: letters and digits without look-alikes (0/O, 1/l/I) so
// passwords can be read off a screen and typed on a phone.
const passwordAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GeneratePassword returns a random password of n characters.
func GeneratePassword(n int) string {
	b := make([]byte, n)
	max := big.NewInt(int64(len(passwordAlphabet)))
	for i := range b {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		b[i] = passwordAlphabet[v.Int64()]
	}
	return string(b)
}

// NewID returns a random identifier.
func NewID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// rcloneObscureKey is the fixed key rclone uses to obscure passwords in its
// config file (fs/config/obscure in the rclone source). Obscuring is not
// encryption; it only stops passwords being readable at a glance.
var rcloneObscureKey = []byte{
	0x9c, 0x93, 0x5b, 0x48, 0x73, 0x0a, 0x55, 0x4d,
	0x6b, 0xfd, 0x7c, 0x63, 0xc8, 0x86, 0xa9, 0x2b,
	0xd3, 0x90, 0x19, 0x8e, 0xb8, 0x12, 0x8a, 0xfb,
	0xf4, 0xde, 0x16, 0x2b, 0x8b, 0x95, 0xf6, 0x38,
}

// Obscure a value the same way `rclone obscure` does, for writing rclone
// config snippets that clients can paste.
func Obscure(x string) string {
	block, err := aes.NewCipher(rcloneObscureKey)
	if err != nil {
		panic(err)
	}
	out := make([]byte, aes.BlockSize+len(x))
	iv := out[:aes.BlockSize]
	if _, err := rand.Read(iv); err != nil {
		panic(err)
	}
	cipher.NewCTR(block, iv).XORKeyStream(out[aes.BlockSize:], []byte(x))
	return base64.RawURLEncoding.EncodeToString(out)
}
