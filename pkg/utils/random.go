package utils

import (
	"crypto/rand"
)

func GenerateRandomString(length int) string {
	if length <= 0 {
		return ""
	}
	const charset = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	const n = byte(len(charset))                       // 62
	const threshold = byte(256 - (256 % len(charset))) // 248

	out := make([]byte, 0, length)
	buf := make([]byte, length)
	for len(out) < length {
		if _, err := rand.Read(buf); err != nil {
			return ""
		}
		for _, b := range buf {
			if b < threshold {
				out = append(out, charset[int(b%n)])
				if len(out) == length {
					break
				}
			}
		}
	}
	return string(out)
}

func GeneratePassword() string {
	return GenerateRandomString(12)
}

func GenerateToken() string {
	return GenerateRandomString(22)
}
