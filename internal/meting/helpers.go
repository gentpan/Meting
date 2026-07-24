package meting

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
)

func md5Hex(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

func base64StdDecode(value string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}
