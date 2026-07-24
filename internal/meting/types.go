package meting

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

var ErrNotFound = errors.New("resource not found")

type ConfigError struct {
	Message string
}

func (e ConfigError) Error() string {
	return e.Message
}

type Song struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Artists  []string `json:"artists"`
	Album    string   `json:"album"`
	Duration int      `json:"duration,omitempty"` // 秒
	CoverID  string   `json:"cover_id,omitempty"`
	StreamID string   `json:"stream_id,omitempty"`
	LyricID  string   `json:"lyric_id,omitempty"`
	Provider string   `json:"provider"`
}

type Item struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Artist   string   `json:"artist"`
	Artists  []string `json:"artists"`
	Album    string   `json:"album"`
	Duration int      `json:"duration,omitempty"` // 秒
	URL      string   `json:"url"`
	Cover    string   `json:"cover"`
	Lyric    string   `json:"lyric"`
	Provider string   `json:"provider"`
}

func (s Song) Item(baseURL, prefix string) Item {
	baseURL = strings.TrimRight(baseURL, "/")
	if prefix == "" {
		prefix = "/api/v1"
	}
	prefix = "/" + strings.Trim(prefix, "/")
	songID := url.PathEscape(s.ID)

	return Item{
		ID:       s.ID,
		Name:     s.Name,
		Artist:   strings.Join(s.Artists, "/"),
		Artists:  append([]string(nil), s.Artists...),
		Album:    s.Album,
		Duration: s.Duration,
		URL:      fmt.Sprintf("%s%s/%s/songs/%s/stream", baseURL, prefix, s.Provider, songID),
		Cover:    fmt.Sprintf("%s%s/%s/songs/%s/cover", baseURL, prefix, s.Provider, songID),
		Lyric:    fmt.Sprintf("%s%s/%s/songs/%s/lyric", baseURL, prefix, s.Provider, songID),
		Provider: s.Provider,
	}
}

type Stream struct {
	URL     string `json:"url"`
	Size    int64  `json:"size,omitempty"`
	BR      int    `json:"br,omitempty"`
	Format  string `json:"format,omitempty"`
	Quality string `json:"quality,omitempty"`
}

const (
	QualityStandard = "standard"
	QualityExhigh   = "exhigh"
	QualityLossless = "lossless"
	QualityHires    = "hires"
	QualityJyeffect = "jyeffect"
	QualitySky      = "sky"
	QualityDolby    = "dolby"
	QualityJymaster = "jymaster"
)

func NormalizeQuality(q string) string {
	switch strings.ToLower(strings.TrimSpace(q)) {
	case "", "default", "auto":
		return QualityExhigh
	case QualityStandard, "128":
		return QualityStandard
	case "higher", "192":
		return QualityExhigh
	case QualityExhigh, "hq", "320":
		return QualityExhigh
	case QualityLossless, "sq", "flac":
		return QualityLossless
	case QualityHires, "hr":
		return QualityHires
	case QualityJyeffect:
		return QualityJyeffect
	case QualitySky:
		return QualitySky
	case QualityDolby:
		return QualityDolby
	case QualityJymaster, "master":
		return QualityJymaster
	default:
		return QualityExhigh
	}
}

type Lyrics struct {
	Primary    string `json:"primary"`
	Translated string `json:"translated,omitempty"`
	Merged     string `json:"merged"`
}

type ProviderDescriptor struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Status      string   `json:"status"`
	Resources   []string `json:"resources"`
	Notes       string   `json:"notes"`
}

type PlaylistDetail struct {
	ID          string
	Name        string
	Cover       string
	Description string
	Songs       []Song
}

type PlaylistDetailProvider interface {
	Provider
	PlaylistDetail(id string) (PlaylistDetail, error)
}

type AlbumDetail struct {
	ID          string
	Name        string
	Cover       string
	Artist      string   // joined artist names ("a / b")
	Artists     []string // individual names
	Publish     string   // release date, RFC3339 or "YYYY-MM-DD"
	Description string
	Company     string
	Songs       []Song
}

type AlbumDetailProvider interface {
	Provider
	AlbumDetail(id string) (AlbumDetail, error)
}

type ArtistDetail struct {
	ID          string
	Name        string
	Cover       string
	Description string
	SongCount   int
	AlbumCount  int
	Songs       []Song
}

type ArtistDetailProvider interface {
	Provider
	ArtistDetail(id string, limit int) (ArtistDetail, error)
}

type Provider interface {
	Meta() ProviderDescriptor
	Search(keyword string, page, limit int) ([]Song, error)
	Song(id string) (Song, error)
	Playlist(id string) ([]Song, error)
	Album(id string) ([]Song, error)
	Artist(id string, limit int) ([]Song, error)
	Stream(id, quality string) (Stream, error)
	Cover(id string, size int) (string, error)
	Lyric(id string) (Lyrics, error)
}

func buildItems(baseURL, prefix string, songs []Song) []Item {
	items := make([]Item, 0, len(songs))
	for _, song := range songs {
		items = append(items, song.Item(baseURL, prefix))
	}
	return items
}

func mergeLyrics(primary, translated string) string {
	primary = strings.TrimSpace(primary)
	translated = strings.TrimSpace(translated)
	if primary == "" {
		return translated
	}
	if translated == "" {
		return primary
	}

	type lyricLine struct {
		ts   string
		text string
		raw  string
	}

	parse := func(text string) []lyricLine {
		lines := strings.Split(text, "\n")
		result := make([]lyricLine, 0, len(lines))
		for _, line := range lines {
			line = strings.TrimRight(line, "\r")
			if line == "" {
				continue
			}
			ts := ""
			body := line
			if strings.HasPrefix(line, "[") {
				if end := strings.Index(line, "]"); end > 0 {
					ts = line[:end+1]
					body = line[end+1:]
				}
			}
			result = append(result, lyricLine{ts: ts, text: body, raw: line})
		}
		return result
	}

	translatedLines := parse(translated)
	translatedMap := make(map[string][]string, len(translatedLines))
	translatedOrder := make([]string, 0, len(translatedLines))
	for _, line := range translatedLines {
		if line.ts == "" {
			continue
		}
		if _, ok := translatedMap[line.ts]; !ok {
			translatedOrder = append(translatedOrder, line.ts)
		}
		translatedMap[line.ts] = append(translatedMap[line.ts], line.text)
	}

	used := make(map[string]bool, len(translatedMap))
	merged := make([]string, 0, len(translatedLines)*2)
	for _, line := range parse(primary) {
		merged = append(merged, line.raw)
		if line.ts == "" {
			continue
		}
		entries := translatedMap[line.ts]
		for _, translatedText := range entries {
			trimmed := strings.TrimSpace(translatedText)
			if trimmed == "" || trimmed == strings.TrimSpace(line.text) {
				continue
			}
			merged = append(merged, line.ts+translatedText)
			used[line.ts] = true
		}
	}

	for _, ts := range translatedOrder {
		if used[ts] {
			continue
		}
		for _, translatedText := range translatedMap[ts] {
			trimmed := strings.TrimSpace(translatedText)
			if trimmed == "" {
				continue
			}
			merged = append(merged, ts+translatedText)
		}
	}

	return strings.Join(merged, "\n")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func normalizeURL(value string) string {
	switch {
	case strings.HasPrefix(value, "//"):
		return "https:" + value
	case strings.HasPrefix(value, "http://"):
		return "https://" + strings.TrimPrefix(value, "http://")
	default:
		return value
	}
}

func sanitizeArtists(values []string) []string {
	artists := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		artists = append(artists, value)
	}
	return artists
}

func sortProviders(providers []ProviderDescriptor) {
	sort.Slice(providers, func(i, j int) bool {
		return providers[i].Name < providers[j].Name
	})
}
