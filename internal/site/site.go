package site

import (
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"metingio/internal/auth"
	"metingio/internal/meting"
)

//go:embed static static/assets/*
var embedded embed.FS

func NewHandler(svc *meting.Service, store *auth.Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		allowCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/api/v1/providers", func(w http.ResponseWriter, r *http.Request) {
		allowCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"providers": svc.Providers()})
	})
	mux.HandleFunc("/api/v1/token", func(w http.ResponseWriter, r *http.Request) {
		allowCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "use POST to issue a token")
			return
		}
		handleIssueToken(store, w, r)
	})
	mux.HandleFunc("/api/v1/stats", func(w http.ResponseWriter, r *http.Request) {
		allowCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		handleStats(store, w, r)
	})
	mux.HandleFunc("/api/v1/admin/", func(w http.ResponseWriter, r *http.Request) {
		allowCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		handleAdmin(store, w, r)
	})
	mux.HandleFunc("/api/v1/", func(w http.ResponseWriter, r *http.Request) {
		allowCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !authorize(store, w, r) {
			return
		}
		handleAPI(svc, w, r, "v1")
	})
	// v2 = high-definition / quality-selectable tier. Operator token only:
	// public tokens stay on v1 (mp3) to protect bandwidth and the shared accounts.
	mux.HandleFunc("/api/v2/", func(w http.ResponseWriter, r *http.Request) {
		allowCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !authorize(store, w, r) {
			return
		}
		if !store.IsSpecial(auth.ExtractToken(r)) {
			writeError(w, http.StatusForbidden, "v2 (高清/无损) 仅限运营 token;公开 token 请使用 v1 (mp3)")
			return
		}
		handleAPI(svc, w, r, "v2")
	})
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(mustSub("static/assets")))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/index.html":
			serveFile(w, "static/index.html")
		case "/providers.html":
			serveFile(w, "static/providers.html")
		case "/admin", "/admin.html":
			serveFile(w, "static/admin.html")
		default:
			if strings.HasPrefix(r.URL.Path, "/assets/") || strings.HasPrefix(r.URL.Path, "/api/") {
				http.NotFound(w, r)
				return
			}
			serveFile(w, "static/index.html")
		}
	})
	return withRequestLog(store, mux)
}

// withRequestLog wraps the mux so every /api/v1/* and /healthz call bumps the
// counters table by (day, provider, resource). No PII, no token, no IP.
func withRequestLog(store *auth.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		countable := (path == "/healthz" || strings.HasPrefix(path, "/api/v1") || strings.HasPrefix(path, "/api/v2")) &&
			!strings.HasPrefix(path, "/api/v1/admin")
		if countable && r.Method != http.MethodOptions {
			provider, resource := splitAPIPath(path)
			store.IncrementCounter(provider, resource)
		}
		next.ServeHTTP(w, r)
	})
}

// splitAPIPath extracts the provider and resource name from an /api/v1 path.
// Examples:
//
//	/api/v1/netease/search        → ("netease", "search")
//	/api/v1/tencent/songs/123     → ("tencent", "songs")
//	/api/v1/providers             → ("", "providers")
//	/api/v1/token                 → ("", "token")
//	/healthz                      → ("", "healthz")
func splitAPIPath(path string) (provider, resource string) {
	if path == "/healthz" {
		return "", "healthz"
	}
	trimmed := strings.TrimPrefix(strings.TrimPrefix(path, "/api/v1/"), "/api/v2/")
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" {
		return "", ""
	}
	parts := strings.SplitN(trimmed, "/", 3)
	switch parts[0] {
	case "providers", "token", "stats":
		return "", parts[0]
	}
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}

// handleStats returns aggregated request stats. Protected by the special
// token to keep statistics private to the operator.
func handleStats(store *auth.Store, w http.ResponseWriter, r *http.Request) {
	token := auth.ExtractToken(r)
	if token == "" || !store.IsSpecial(token) {
		writeError(w, http.StatusForbidden, "stats endpoint requires the special operator token")
		return
	}
	days := parseInt(r.URL.Query().Get("days"), 7)
	if days > 90 {
		days = 90
	}
	summary, err := store.StatsSummary(days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stats query failed")
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// capPublicQuality limits non-special-token requests to standard / exhigh mp3.
// Any higher tier (lossless, hires, dolby, sky, jymaster) is silently downgraded
// to exhigh (320 kbps mp3) — keeps audio web-playable and avoids serving lossless
// to anonymous public tokens.
func capPublicQuality(q string) string {
	switch meting.NormalizeQuality(q) {
	case meting.QualityStandard:
		return meting.QualityStandard
	default:
		return meting.QualityExhigh
	}
}

func handleAPI(svc *meting.Service, w http.ResponseWriter, r *http.Request, version string) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/"+version+"/")
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) < 2 {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}

	provider, err := svc.Provider(parts[0])
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	baseURL := svc.BaseURL(r)
	urlPrefix := svc.URLPrefix(r)

	switch {
	case len(parts) == 2 && parts[1] == "search":
		page := parseInt(r.URL.Query().Get("page"), 1)
		limit := parseInt(r.URL.Query().Get("limit"), 100)
		query := r.URL.Query().Get("q")
		if query == "" {
			query = r.URL.Query().Get("keyword")
		}
		songs, err := provider.Search(query, page, limit)
		if err != nil {
			handleProviderError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"provider": parts[0],
			"kind":     "search",
			"query":    query,
			"page":     page,
			"limit":    limit,
			"count":    len(songs),
			"items":    metingBuildItems(baseURL, urlPrefix, songs),
			"songs":    songs,
		})
	case len(parts) == 3 && parts[1] == "playlists":
		handlePlaylist(w, provider, baseURL, urlPrefix, parts[0], parts[2])
	case len(parts) == 3 && parts[1] == "albums":
		handleAlbum(w, provider, baseURL, urlPrefix, parts[0], parts[2])
	case len(parts) == 3 && parts[1] == "artists":
		limit := parseInt(r.URL.Query().Get("limit"), 50)
		handleArtist(w, provider, baseURL, urlPrefix, parts[0], parts[2], limit)
	case len(parts) == 3 && parts[1] == "songs":
		song, err := provider.Song(parts[2])
		if err != nil {
			handleProviderError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"provider": parts[0],
			"id":       parts[2],
			"item":     song.Item(baseURL, urlPrefix),
			"song":     song,
		})
	case len(parts) == 4 && parts[1] == "songs" && parts[3] == "stream":
		quality := r.URL.Query().Get("quality")
		if version == "v2" {
			// v2 = HD / selectable: honour the requested quality, default to lossless.
			if strings.TrimSpace(quality) == "" {
				quality = meting.QualityLossless
			}
		} else {
			// v1 = web default: always mp3 (standard/exhigh), regardless of token.
			quality = capPublicQuality(quality)
		}
		stream, err := provider.Stream(parts[2], quality)
		if err != nil {
			handleProviderError(w, err)
			return
		}
		if r.URL.Query().Get("redirect") == "false" {
			writeJSON(w, http.StatusOK, stream)
			return
		}
		// Proxy mode: fetch audio and stream to client (avoids CORS)
		if r.URL.Query().Get("proxy") == "true" || r.Header.Get("Origin") != "" {
			proxyReq, _ := http.NewRequest("GET", stream.URL, nil)
			proxyReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36")
			// Set correct referer based on platform
			switch parts[0] {
			case "netease":
				proxyReq.Header.Set("Referer", "https://music.163.com/")
			case "tencent":
				proxyReq.Header.Set("Referer", "https://y.qq.com/")
			case "kugou":
				proxyReq.Header.Set("Referer", "https://www.kugou.com/")
			default:
				proxyReq.Header.Set("Referer", stream.URL)
			}
			// 大文件（lossless FLAC 可达 40MB+）下载需要时间；保留 10 分钟兜底。
			client := &http.Client{Timeout: 10 * time.Minute}
			resp, err := client.Do(proxyReq)
			if err != nil {
				http.Error(w, "proxy error", 502)
				return
			}
			defer resp.Body.Close()
			w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
			if cl := resp.Header.Get("Content-Length"); cl != "" {
				w.Header().Set("Content-Length", cl)
			}
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Cache-Control", "public, max-age=3600")
			io.Copy(w, resp.Body)
			return
		}
		http.Redirect(w, r, stream.URL, http.StatusFound)
	case len(parts) == 4 && parts[1] == "songs" && parts[3] == "cover":
		size := parseInt(r.URL.Query().Get("size"), 300)
		coverURL, err := provider.Cover(parts[2], size)
		if err != nil {
			handleProviderError(w, err)
			return
		}
		if r.URL.Query().Get("redirect") == "false" {
			writeJSON(w, http.StatusOK, map[string]string{"url": coverURL})
			return
		}
		http.Redirect(w, r, coverURL, http.StatusFound)
	case len(parts) == 4 && parts[1] == "songs" && parts[3] == "lyric":
		lyrics, err := provider.Lyric(parts[2])
		if err != nil {
			handleProviderError(w, err)
			return
		}
		if r.URL.Query().Get("format") == "json" {
			writeJSON(w, http.StatusOK, lyrics)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(lyrics.Merged))
	default:
		writeError(w, http.StatusNotFound, "resource not found")
	}
}

func handleAlbum(w http.ResponseWriter, provider meting.Provider, baseURL, urlPrefix, providerName, id string) {
	if adp, ok := provider.(meting.AlbumDetailProvider); ok {
		detail, err := adp.AlbumDetail(id)
		if err != nil {
			handleProviderError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"provider":    providerName,
			"kind":        "album",
			"id":          detail.ID,
			"name":        detail.Name,
			"cover":       detail.Cover,
			"artist":      detail.Artist,
			"artists":     detail.Artists,
			"publish":     detail.Publish,
			"description": detail.Description,
			"company":     detail.Company,
			"count":       len(detail.Songs),
			"items":       metingBuildItems(baseURL, urlPrefix, detail.Songs),
			"songs":       detail.Songs,
		})
		return
	}
	handleCollection(w, provider, baseURL, urlPrefix, providerName, "album", id, provider.Album)
}

func handleArtist(w http.ResponseWriter, provider meting.Provider, baseURL, urlPrefix, providerName, id string, limit int) {
	if adp, ok := provider.(meting.ArtistDetailProvider); ok {
		detail, err := adp.ArtistDetail(id, limit)
		if err != nil {
			handleProviderError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"provider":    providerName,
			"kind":        "artist",
			"id":          detail.ID,
			"name":        detail.Name,
			"cover":       detail.Cover,
			"description": detail.Description,
			"song_count":  detail.SongCount,
			"album_count": detail.AlbumCount,
			"count":       len(detail.Songs),
			"items":       metingBuildItems(baseURL, urlPrefix, detail.Songs),
			"songs":       detail.Songs,
		})
		return
	}
	songs, err := provider.Artist(id, limit)
	if err != nil {
		handleProviderError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"provider": providerName,
		"kind":     "artist",
		"id":       id,
		"count":    len(songs),
		"items":    metingBuildItems(baseURL, urlPrefix, songs),
		"songs":    songs,
	})
}

func handlePlaylist(w http.ResponseWriter, provider meting.Provider, baseURL, urlPrefix, providerName, id string) {
	if pdp, ok := provider.(meting.PlaylistDetailProvider); ok {
		detail, err := pdp.PlaylistDetail(id)
		if err != nil {
			handleProviderError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"provider":    providerName,
			"kind":        "playlist",
			"id":          detail.ID,
			"name":        detail.Name,
			"cover":       detail.Cover,
			"description": detail.Description,
			"count":       len(detail.Songs),
			"items":       metingBuildItems(baseURL, urlPrefix, detail.Songs),
			"songs":       detail.Songs,
		})
		return
	}
	handleCollection(w, provider, baseURL, urlPrefix, providerName, "playlist", id, provider.Playlist)
}

func handleCollection(
	w http.ResponseWriter,
	provider meting.Provider,
	baseURL, urlPrefix, providerName, kind, id string,
	fn func(string) ([]meting.Song, error),
) {
	songs, err := fn(id)
	if err != nil {
		handleProviderError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"provider": providerName,
		"kind":     kind,
		"id":       id,
		"count":    len(songs),
		"items":    metingBuildItems(baseURL, urlPrefix, songs),
		"songs":    songs,
	})
}

func metingBuildItems(baseURL, urlPrefix string, songs []meting.Song) []meting.Item {
	items := make([]meting.Item, 0, len(songs))
	for _, song := range songs {
		items = append(items, song.Item(baseURL, urlPrefix))
	}
	return items
}

func handleProviderError(w http.ResponseWriter, err error) {
	var cfgErr meting.ConfigError
	switch {
	case errors.Is(err, meting.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.As(err, &cfgErr):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
}

func allowCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Expose-Headers", "X-RateLimit-Limit, X-RateLimit-Remaining, X-RateLimit-Reset")
}

// authorize checks the request's bearer token, increments quota, and writes
// rate-limit headers. Returns false (and writes an error response) if the
// request is rejected.
func authorize(store *auth.Store, w http.ResponseWriter, r *http.Request) bool {
	token := auth.ExtractToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "token required; POST /v1/token to apply")
		return false
	}
	limit, _, remaining, err := store.CheckAndConsume(token)
	if errors.Is(err, auth.ErrTokenInvalid) {
		writeError(w, http.StatusUnauthorized, "invalid token")
		return false
	}
	if errors.Is(err, auth.ErrQuotaExceeded) {
		auth.WriteRateHeaders(w, limit, 0)
		w.Header().Set("Retry-After", strconv.FormatInt(auth.NextResetUnix()-time.Now().UTC().Unix(), 10))
		writeError(w, http.StatusTooManyRequests, "daily quota exceeded")
		return false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth check failed")
		return false
	}
	auth.WriteRateHeaders(w, limit, remaining)
	return true
}

func handleIssueToken(store *auth.Store, w http.ResponseWriter, r *http.Request) {
	ip := auth.ClientIP(r)
	token, err := store.IssueToken(ip)
	if errors.Is(err, auth.ErrIssuanceLimited) {
		writeError(w, http.StatusTooManyRequests, "token already issued from this IP today")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":       token,
		"daily_limit": store.DailyLimit(),
		"reset":       "UTC 00:00",
		"reset_unix":  auth.NextResetUnix(),
		"usage":       "send as 'Authorization: Bearer <token>' header",
		"note":        "fetched data is yours to keep; only API calls are rate-limited",
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func parseInt(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func serveFile(w http.ResponseWriter, name string) {
	data, err := embedded.ReadFile(name)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	switch path.Ext(name) {
	case ".html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case ".js":
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	}
	_, _ = w.Write(data)
}

func mustSub(name string) fs.FS {
	sub, err := fs.Sub(embedded, name)
	if err != nil {
		panic(err)
	}
	return sub
}
