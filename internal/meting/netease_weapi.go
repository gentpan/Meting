package meting

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
)

// NetEase weapi encryption.
// Reference: https://github.com/Binaryify/NeteaseCloudMusicApi (well-documented).
const (
	weapiNonce   = "0CoJUm6Qyw8W8jud"
	weapiIV      = "0102030405060708"
	weapiPubkey  = "010001"
	weapiModulus = "00e0b509f6259df8642dbc35662901477df22677ec152b5ff68ace615bb7b725152b3ab17a876aea8a5aa76d2e417629ec4ee341f56135fccf695280104e0312ecbda92557c93870114af6c9d05c4f7f0c3685b7a46bee255932575cce10b424d813cfe4875d3e82047b97ddef52741d546b8e289dc6935b3ece0462db0a22b8e7"
)

func aesCBCEncrypt(plaintext, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	pad := block.BlockSize() - len(plaintext)%block.BlockSize()
	padded := append(plaintext, bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return out, nil
}

func weapiRSAEncrypt(text string) string {
	rev := []byte(text)
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	hexStr := hex.EncodeToString(rev)
	msg, _ := new(big.Int).SetString(hexStr, 16)
	n, _ := new(big.Int).SetString(weapiModulus, 16)
	e, _ := new(big.Int).SetString(weapiPubkey, 16)
	enc := new(big.Int).Exp(msg, e, n)
	return fmt.Sprintf("%0256x", enc)
}

func weapiRandomSecret() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, 16)
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	for i := range out {
		out[i] = charset[int(buf[i])%len(charset)]
	}
	return string(out)
}

// weapiEncrypt produces (params, encSecKey) for netease's weapi protocol.
func weapiEncrypt(payloadJSON string) (string, string, error) {
	secret := weapiRandomSecret()
	first, err := aesCBCEncrypt([]byte(payloadJSON), []byte(weapiNonce), []byte(weapiIV))
	if err != nil {
		return "", "", err
	}
	firstB64 := base64.StdEncoding.EncodeToString(first)
	second, err := aesCBCEncrypt([]byte(firstB64), []byte(secret), []byte(weapiIV))
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(second), weapiRSAEncrypt(secret), nil
}
