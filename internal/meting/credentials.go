package meting

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/guohuiyuan/music-lib/kugou"
	"github.com/guohuiyuan/music-lib/model"
	"github.com/guohuiyuan/music-lib/netease"
	"github.com/guohuiyuan/music-lib/qq"
)

// Credentials supplies the live cookie for a provider at request time, so a
// freshly scanned login takes effect without restarting the service. The
// concrete implementation is the SQLite-backed auth.Store.
type Credentials interface {
	Cookie(provider string) string
}

// QRLoginSource is one scannable login source exposed by the admin backend.
// Source is what the client sends; Provider is the credential key the resulting
// cookie is stored under (QQ scan and WeChat scan both feed the "tencent" key).
type QRLoginSource struct {
	Source      string `json:"source"`
	DisplayName string `json:"display_name"`
	Provider    string `json:"provider"`
}

// QRLoginSources lists every supported scan source.
func QRLoginSources() []QRLoginSource {
	return []QRLoginSource{
		{Source: "netease", DisplayName: "网易云音乐", Provider: "netease"},
		{Source: "qq", DisplayName: "QQ音乐 (QQ扫码)", Provider: "tencent"},
		{Source: "qq_wx", DisplayName: "QQ音乐 (微信扫码)", Provider: "tencent"},
		{Source: "kugou", DisplayName: "酷狗音乐", Provider: "kugou"},
	}
}

// NormalizeCookieInput turns whatever the operator pastes into a clean,
// single-line "k=v; k=v" cookie header. It accepts three shapes:
//   - a raw cookie string (returned as-is, with newlines stripped)
//   - a browser cookie-export JSON array of {"name","value",...} objects
//   - a name->value JSON object (or a {"cookie":"..."} wrapper)
//
// This exists because cookie-export browser extensions emit pretty-printed JSON,
// and pasting that verbatim previously produced an invalid Cookie header.
func NormalizeCookieInput(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	switch s[0] {
	case '[':
		var arr []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal([]byte(s), &arr); err == nil {
			pairs := make([]string, 0, len(arr))
			for _, c := range arr {
				if n := strings.TrimSpace(c.Name); n != "" {
					pairs = append(pairs, n+"="+sanitizeCookieValue(c.Value))
				}
			}
			if len(pairs) > 0 {
				return strings.Join(pairs, "; ")
			}
		}
	case '{':
		var m map[string]string
		if err := json.Unmarshal([]byte(s), &m); err == nil {
			if c, ok := m["cookie"]; ok && len(m) == 1 {
				return sanitizeCookieLine(c)
			}
			pairs := make([]string, 0, len(m))
			for k, v := range m {
				if k = strings.TrimSpace(k); k != "" {
					pairs = append(pairs, k+"="+sanitizeCookieValue(v))
				}
			}
			sort.Strings(pairs)
			if len(pairs) > 0 {
				return strings.Join(pairs, "; ")
			}
		}
	}
	return sanitizeCookieLine(s)
}

func sanitizeCookieValue(v string) string {
	return strings.NewReplacer("\r", "", "\n", "", ";", "").Replace(strings.TrimSpace(v))
}

func sanitizeCookieLine(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(s))
}

// CredentialProviders returns the distinct provider keys that hold a cookie,
// in display order. Used by the admin backend to enumerate account slots.
func CredentialProviders() []string {
	return []string{"netease", "tencent", "kugou"}
}

// CredentialKeyForSource maps a scan source to the provider key its cookie is
// stored under. Returns false for unknown sources.
func CredentialKeyForSource(source string) (string, bool) {
	for _, s := range QRLoginSources() {
		if s.Source == source {
			return s.Provider, true
		}
	}
	return "", false
}

// CreateQR starts a scan session for the given source. The returned session's
// Key carries all state the matching CheckQR call needs (qrsig / unikey / uuid /
// qrcode), so polling is stateless across requests.
func CreateQR(source string) (*model.QRLoginSession, error) {
	switch source {
	case "netease":
		return netease.New("").CreateQRLogin()
	case "qq":
		return qq.New("").CreateQRLogin()
	case "qq_wx":
		return qq.New("").CreateWXQRLogin()
	case "kugou":
		return kugou.New("").CreateQRLogin()
	default:
		return nil, fmt.Errorf("unsupported qr login source: %s", source)
	}
}

// CheckQR polls a scan session. On success, result.Cookie holds the cookie to
// persist for the source's provider key.
func CheckQR(source, key string) (*model.QRLoginResult, error) {
	switch source {
	case "netease":
		return netease.New("").CheckQRLogin(key)
	case "qq":
		return qq.New("").CheckQRLogin(key)
	case "qq_wx":
		return qq.New("").CheckWXQRLogin(key)
	case "kugou":
		return kugou.New("").CheckQRLogin(key)
	default:
		return nil, fmt.Errorf("unsupported qr login source: %s", source)
	}
}

// HealthResult is the outcome of actively probing a stored cookie.
type HealthResult struct {
	Status    string // alive | invalid | unknown
	Detail    string
	VIPExpiry string
}

// CheckHealth actively probes whether a provider's cookie is still usable.
//
// For netease it calls the cookie-only account endpoint, which returns the
// logged-in account id — a true login/expiry signal. For kugou and QQ music-lib
// only exposes IsVipAccount, which cannot distinguish "expired" from "valid but
// not a member", so we interpret it conservatively: a clean VIP=true is "alive",
// a transport error is "invalid", and VIP=false is "unknown" (never a false
// green). The operator adds accounts to unlock VIP streaming, so VIP=true is the
// signal that matters.
func CheckHealth(provider, cookie string) HealthResult {
	cookie = strings.TrimSpace(cookie)
	if cookie == "" {
		return HealthResult{Status: "unknown", Detail: "未配置 cookie"}
	}
	switch provider {
	case "netease":
		return neteaseHealth(cookie)
	case "kugou":
		return kugouHealth(cookie)
	case "tencent":
		return conservativeVIP(qq.New(cookie).IsVipAccount())
	default:
		return HealthResult{Status: "unknown", Detail: "不支持健康探测"}
	}
}

func conservativeVIP(vip bool, err error) HealthResult {
	if err != nil {
		return HealthResult{Status: "invalid", Detail: "探测失败,cookie 可能已失效: " + err.Error()}
	}
	if vip {
		return HealthResult{Status: "alive", Detail: "登录有效 · VIP"}
	}
	return HealthResult{Status: "unknown", Detail: "接口可访问但非会员,无法确认登录态(建议重新扫码)"}
}

// kugouHealth probes a kugou APP-token cookie (issued by QR login). The web VIP
// endpoint that IsVipAccount uses cannot see app tokens, so it would falsely
// report "non-member". Instead we exercise the real capability: search a popular
// keyword and try to resolve a direct URL via the app token. A URL back means
// the token is live; a lossless URL (flac/ape) confirms VIP entitlement.
func kugouHealth(cookie string) HealthResult {
	k := kugou.New(cookie)
	songs, err := k.Search("周杰伦")
	if err != nil || len(songs) == 0 {
		// search failed — fall back to the web probe rather than claim dead
		return conservativeVIP(k.IsVipAccount())
	}
	resolvedExt := ""
	for i := range songs {
		if i >= 4 {
			break
		}
		song := songs[i]
		rawURL, derr := k.GetDownloadURL(&song)
		if derr != nil || strings.TrimSpace(rawURL) == "" {
			continue
		}
		switch extFromURL(rawURL) {
		case "flac", "ape", "wav":
			return HealthResult{Status: "alive", Detail: "登录有效 · VIP(无损直链可取)"}
		default:
			if resolvedExt == "" {
				resolvedExt = extFromURL(rawURL)
			}
		}
	}
	if resolvedExt != "" {
		return HealthResult{Status: "alive", Detail: "登录有效 · 直链可取(" + resolvedExt + ",未取到无损)"}
	}
	return HealthResult{Status: "invalid", Detail: "已登录但取不到直链,cookie / 会员可能已失效"}
}

// neteaseHealth hits the cookie-only account endpoint. A present account id means
// the cookie is logged in; null means it expired or was never valid.
func neteaseHealth(cookie string) HealthResult {
	data, err := httpGet(context.Background(), "https://music.163.com/api/nuser/account/get", nil, map[string]string{
		"Cookie":     cookie,
		"Referer":    "https://music.163.com/",
		"User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
	})
	if err != nil {
		return HealthResult{Status: "invalid", Detail: "探测失败: " + err.Error()}
	}
	var resp struct {
		Code    int `json:"code"`
		Account *struct {
			ID int64 `json:"id"`
		} `json:"account"`
		Profile *struct {
			Nickname string `json:"nickname"`
			VipType  int    `json:"vipType"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return HealthResult{Status: "invalid", Detail: "返回解析失败,cookie 可能已失效"}
	}
	if resp.Account == nil || resp.Account.ID == 0 {
		return HealthResult{Status: "invalid", Detail: "未登录 / cookie 已失效"}
	}
	nick := ""
	vip := 0
	if resp.Profile != nil {
		nick = resp.Profile.Nickname
		vip = resp.Profile.VipType
	}
	if vip > 0 {
		return HealthResult{Status: "alive", Detail: "登录有效 · VIP · " + nick}
	}
	return HealthResult{Status: "alive", Detail: "登录有效 · 非 VIP · " + nick}
}

// streamViaMusicLib resolves a playable URL through music-lib using the provider
// key (kugou/netease/tencent), the meting song id, and the live cookie. meting's
// search ids already match what music-lib expects (kugou=hash, netease=numeric
// id, tencent=songmid), so no id translation is needed.
func streamViaMusicLib(providerKey, id, cookie string) (Stream, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Stream{}, ErrNotFound
	}
	var (
		rawURL string
		err    error
	)
	switch providerKey {
	case "kugou":
		song := &model.Song{Source: "kugou", ID: id, Extra: map[string]string{"hash": id}}
		rawURL, err = kugou.New(cookie).GetDownloadURL(song)
	case "netease":
		song := &model.Song{Source: "netease", ID: id, Extra: map[string]string{"song_id": id}}
		rawURL, err = netease.New(cookie).GetDownloadURL(song)
	case "tencent":
		song := &model.Song{Source: "qq", ID: id, Extra: map[string]string{"songmid": id}}
		rawURL, err = qq.New(cookie).GetDownloadURL(song)
	default:
		return Stream{}, ErrNotFound
	}
	if err != nil {
		return Stream{}, err
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return Stream{}, ErrNotFound
	}
	return Stream{URL: rawURL, Format: extFromURL(rawURL)}, nil
}

// extFromURL guesses a media extension from a URL path, defaulting to mp3.
func extFromURL(u string) string {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	if dot := strings.LastIndex(u, "."); dot >= 0 {
		ext := strings.ToLower(u[dot+1:])
		if len(ext) >= 2 && len(ext) <= 4 {
			return ext
		}
	}
	return "mp3"
}
