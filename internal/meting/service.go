package meting

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

type Config struct {
	Port          string
	PublicBaseURL string
	NeteaseCookie string
	TencentCookie string
	KugouCookie   string

	TokenDBPath  string
	DailyLimit   int
	MusicBiToken string
}

func LoadConfigFromEnv() Config {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "18087"
	}
	dailyLimit := 100
	if v := strings.TrimSpace(os.Getenv("DAILY_LIMIT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			dailyLimit = n
		}
	}
	dbPath := strings.TrimSpace(os.Getenv("TOKEN_DB_PATH"))
	if dbPath == "" {
		dbPath = "/var/lib/metingio/tokens.db"
	}
	return Config{
		Port:          port,
		PublicBaseURL: strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")),
		NeteaseCookie: strings.TrimSpace(os.Getenv("NETEASE_COOKIE")),
		TencentCookie: strings.TrimSpace(os.Getenv("TENCENT_COOKIE")),
		KugouCookie:   strings.TrimSpace(os.Getenv("KUGOU_COOKIE")),
		TokenDBPath:   dbPath,
		DailyLimit:    dailyLimit,
		MusicBiToken:  strings.TrimSpace(os.Getenv("MUSICBI_TOKEN")),
	}
}

type Service struct {
	cfg       Config
	creds     Credentials
	providers map[string]Provider
}

// NewService builds the provider set. creds (may be nil) supplies live cookies at
// request time so a freshly scanned login takes effect without a restart; when
// nil or empty for a provider, the static *_COOKIE env value is used.
func NewService(cfg Config, creds Credentials) (*Service, error) {
	tencent, err := NewTencentProvider(cfg, creds)
	if err != nil {
		return nil, err
	}
	return &Service{
		cfg:   cfg,
		creds: creds,
		providers: map[string]Provider{
			"netease": NewNeteaseProvider(cfg, creds),
			"tencent": tencent,
			"kugou":   NewKugouProvider(cfg, creds),
		},
	}, nil
}

func (s *Service) Provider(name string) (Provider, error) {
	provider, ok := s.providers[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", name)
	}
	return provider, nil
}

func (s *Service) Providers() []ProviderDescriptor {
	descriptors := make([]ProviderDescriptor, 0, len(s.providers))
	for _, provider := range s.providers {
		descriptors = append(descriptors, provider.Meta())
	}
	sortProviders(descriptors)
	return descriptors
}

func (s *Service) BaseURL(r *http.Request) string {
	scheme := "http"
	if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		scheme = strings.Split(proto, ",")[0]
	} else if r.TLS != nil {
		scheme = "https"
	}
	// Prefer the host the client originally reached, so generated URLs point back
	// through the same proxy that authorised the call. X-Public-Host is set by
	// trusted edge Caddy (intermediate hops can strip X-Forwarded-Host).
	if host := strings.TrimSpace(r.Header.Get("X-Public-Host")); host != "" {
		return fmt.Sprintf("%s://%s", scheme, host)
	}
	if host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); host != "" {
		return fmt.Sprintf("%s://%s", scheme, host)
	}
	if s.cfg.PublicBaseURL != "" {
		return strings.TrimRight(s.cfg.PublicBaseURL, "/")
	}
	return fmt.Sprintf("%s://%s", scheme, r.Host)
}

// URLPrefix returns the path prefix to use when generating URLs in responses.
// Edge proxies can set X-API-Prefix to expose a different public path scheme
// (e.g. /v1 instead of the internal /api/v1).
func (s *Service) URLPrefix(r *http.Request) string {
	if p := strings.TrimSpace(r.Header.Get("X-API-Prefix")); p != "" {
		return "/" + strings.Trim(p, "/")
	}
	// Fall back to the version actually being requested so generated sub-URLs
	// (stream/cover/lyric) stay on the same tier when called without an edge prefix.
	if strings.HasPrefix(r.URL.Path, "/api/v2") {
		return "/api/v2"
	}
	return "/api/v1"
}

func resources() []string {
	return []string{"search", "songs", "playlists", "albums", "artists", "stream", "cover", "lyric"}
}

func cloneStrings(values []string) []string {
	return append([]string(nil), values...)
}

func sortSongsByName(songs []Song) {
	sort.SliceStable(songs, func(i, j int) bool {
		return songs[i].Name < songs[j].Name
	})
}
