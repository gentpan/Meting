package meting

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Reverse-engineered constants from KuGou Android client.
// These are widely published in open-source projects (e.g. KuGouMusicApi).
const (
	kgAndroidSecret     = "OIlwieks28dk2k092lksi2UIkp"
	kgSignKeySecret     = "57ae12eb6890223e355ccfcb74edf70d"
	kgLiteAndroidSecret = "LnT6xpN3khm36zse0QzvmgTZ3waWdRSA"
	kgLiteSignKeySecret = "185672dd44712f60bb1736df5a377e82"
	kgWebSalt           = "NVPh5oo715z5DIWAeQlhMDsWXXQV4hwt"
	kgAppID             = "1005"
	kgClientver         = "11430"
	kgLiteAppID         = "3116"
	kgLiteClientver     = "11440"
	kgWebSrcAppID       = "2919"
	kgWebClientver      = "20000"
	kgWebAppID          = "1014"
)

func kgMD5(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// kgSignKey: md5(hash + secret + appid + mid + userid)
func kgSignKey(hash, mid, userid string) string {
	return kgMD5(strings.ToLower(hash) + kgSignKeySecret + kgAppID + mid + userid)
}

func kgSignKeyLite(hash, mid, userid string) string {
	return kgMD5(strings.ToLower(hash) + kgLiteSignKeySecret + kgAppID + mid + userid)
}

// kgSignAndroid: md5(secret + sorted(k=v) + body + secret)
func kgSignAndroidLite(params map[string]string, body string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(kgLiteAndroidSecret)
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(params[k])
	}
	b.WriteString(body)
	b.WriteString(kgLiteAndroidSecret)
	return kgMD5(b.String())
}

// kgSignWeb: md5(salt + sorted(key=value) + salt) for H5 endpoints.
func kgSignWeb(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(kgWebSalt)
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(params[k])
	}
	b.WriteString(kgWebSalt)
	return kgMD5(b.String())
}

func kgSignAndroid(params map[string]string, body string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(kgAndroidSecret)
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(params[k])
	}
	b.WriteString(body)
	b.WriteString(kgAndroidSecret)
	return kgMD5(b.String())
}

func parseKugouCookie(raw string) map[string]string {
	out := make(map[string]string)
	for _, piece := range strings.Split(raw, ";") {
		piece = strings.TrimSpace(piece)
		if eq := strings.Index(piece, "="); eq > 0 {
			out[piece[:eq]] = piece[eq+1:]
		}
	}
	return out
}

func kgMapQuality(q string) string {
	switch NormalizeQuality(q) {
	case QualityStandard:
		return "128"
	case QualityExhigh:
		return "320"
	case QualityLossless:
		return "flac"
	case QualityHires:
		return "high"
	case QualityDolby:
		return "viper_atmos"
	default:
		return "320"
	}
}

func kgUserAgent() string {
	return "Android15-1070-11083-46-0-DiscoveryDRADProtocol-wifi"
}

// kgClienttime returns a string unix-second clienttime.
func kgClienttime(now int64) string {
	return fmt.Sprintf("%d", now)
}
