package meting

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type NeteaseProvider struct {
	cfg   Config
	creds Credentials
}

func NewNeteaseProvider(cfg Config, creds Credentials) *NeteaseProvider {
	return &NeteaseProvider{cfg: cfg, creds: creds}
}

// cookie returns the live netease cookie: credential store wins, env fallback.
func (p *NeteaseProvider) cookie() string {
	if p.creds != nil {
		if c := strings.TrimSpace(p.creds.Cookie("netease")); c != "" {
			return c
		}
	}
	return p.cfg.NeteaseCookie
}

func (p *NeteaseProvider) Meta() ProviderDescriptor {
	return ProviderDescriptor{
		Name:        "netease",
		DisplayName: "网易云音乐",
		Status:      "partial",
		Resources:   resources(),
		Notes:       "网易云音乐。搜索、单曲、歌单、专辑、歌手、歌词、封面均可用。",
	}
}

func (p *NeteaseProvider) headers() map[string]string {
	headers := map[string]string{
		"Accept":          "application/json, text/plain, */*",
		"Origin":          "https://music.163.com",
		"Referer":         "https://music.163.com/",
		"User-Agent":      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36",
		"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
	}
	if c := p.cookie(); c != "" {
		headers["Cookie"] = c
	}
	return headers
}

type neteaseTrack struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Dt   int64  `json:"dt"` // 时长（毫秒）
	AR   []struct {
		Name string `json:"name"`
	} `json:"ar"`
	AL struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		PicURL string `json:"picUrl"`
		PicStr string `json:"pic_str"`
		Pic    int64  `json:"pic"`
	} `json:"al"`
}

func (p *NeteaseProvider) Search(keyword string, page, limit int) ([]Song, error) {
	if strings.TrimSpace(keyword) == "" {
		return nil, nil
	}
	form := url.Values{
		"s":      {keyword},
		"type":   {"1"},
		"limit":  {strconv.Itoa(limit)},
		"total":  {"true"},
		"offset": {strconv.Itoa((page - 1) * limit)},
	}
	data, err := httpPostForm(context.Background(), "https://music.163.com/api/cloudsearch/pc", form, p.headers())
	if err != nil {
		return nil, err
	}
	var payload struct {
		Result struct {
			Songs []neteaseTrack `json:"songs"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return p.mapTracks(payload.Result.Songs), nil
}

func (p *NeteaseProvider) Song(id string) (Song, error) {
	tracks, err := p.songDetails([]string{id})
	if err != nil {
		return Song{}, err
	}
	if len(tracks) == 0 {
		return Song{}, ErrNotFound
	}
	return p.mapTrack(tracks[0]), nil
}

func (p *NeteaseProvider) fetchPlaylist(id string) (PlaylistDetail, error) {
	data, err := httpGet(context.Background(), "https://music.163.com/api/v6/playlist/detail", map[string]string{
		"id": id,
		"s":  "0",
		"n":  "1000",
		"t":  "0",
	}, p.headers())
	if err != nil {
		return PlaylistDetail{}, err
	}
	var payload struct {
		Playlist struct {
			ID          int64          `json:"id"`
			Name        string         `json:"name"`
			CoverImgURL string         `json:"coverImgUrl"`
			Description string         `json:"description"`
			Tracks      []neteaseTrack `json:"tracks"`
		} `json:"playlist"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return PlaylistDetail{}, err
	}
	return PlaylistDetail{
		ID:          id,
		Name:        payload.Playlist.Name,
		Cover:       payload.Playlist.CoverImgURL,
		Description: payload.Playlist.Description,
		Songs:       p.mapTracks(payload.Playlist.Tracks),
	}, nil
}

func (p *NeteaseProvider) PlaylistDetail(id string) (PlaylistDetail, error) {
	return p.fetchPlaylist(id)
}

func (p *NeteaseProvider) Playlist(id string) ([]Song, error) {
	detail, err := p.fetchPlaylist(id)
	if err != nil {
		return nil, err
	}
	return detail.Songs, nil
}

func (p *NeteaseProvider) Album(id string) ([]Song, error) {
	detail, err := p.AlbumDetail(id)
	if err != nil {
		return nil, err
	}
	return detail.Songs, nil
}

func (p *NeteaseProvider) AlbumDetail(id string) (AlbumDetail, error) {
	data, err := httpGet(context.Background(), "https://music.163.com/api/v1/album/"+id, nil, p.headers())
	if err != nil {
		return AlbumDetail{}, err
	}
	var payload struct {
		Songs []neteaseTrack `json:"songs"`
		Album struct {
			ID          int64  `json:"id"`
			Name        string `json:"name"`
			PicURL      string `json:"picUrl"`
			PublishTime int64  `json:"publishTime"`
			Description string `json:"description"`
			BriefDesc   string `json:"briefDesc"`
			Company     string `json:"company"`
			Artists     []struct {
				Name string `json:"name"`
			} `json:"artists"`
		} `json:"album"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return AlbumDetail{}, err
	}
	artists := make([]string, 0, len(payload.Album.Artists))
	for _, a := range payload.Album.Artists {
		if a.Name != "" {
			artists = append(artists, a.Name)
		}
	}
	publish := ""
	if payload.Album.PublishTime > 0 {
		publish = time.UnixMilli(payload.Album.PublishTime).UTC().Format("2006-01-02")
	}
	desc := payload.Album.Description
	if desc == "" {
		desc = payload.Album.BriefDesc
	}
	return AlbumDetail{
		ID:          id,
		Name:        payload.Album.Name,
		Cover:       payload.Album.PicURL,
		Artist:      strings.Join(artists, " / "),
		Artists:     artists,
		Publish:     publish,
		Description: desc,
		Company:     payload.Album.Company,
		Songs:       p.mapTracks(payload.Songs),
	}, nil
}

func (p *NeteaseProvider) Artist(id string, limit int) ([]Song, error) {
	detail, err := p.ArtistDetail(id, limit)
	if err != nil {
		return nil, err
	}
	return detail.Songs, nil
}

func (p *NeteaseProvider) ArtistDetail(id string, limit int) (ArtistDetail, error) {
	data, err := httpGet(context.Background(), "https://music.163.com/api/artist/"+id, nil, p.headers())
	if err != nil {
		return ArtistDetail{}, err
	}
	var payload struct {
		HotSongs []neteaseTrack `json:"hotSongs"`
		Artist   struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			PicURL    string `json:"picUrl"`
			BriefDesc string `json:"briefDesc"`
			MusicSize int    `json:"musicSize"`
			AlbumSize int    `json:"albumSize"`
		} `json:"artist"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return ArtistDetail{}, err
	}
	tracks := payload.HotSongs
	if limit > 0 && len(tracks) > limit {
		tracks = tracks[:limit]
	}
	return ArtistDetail{
		ID:          id,
		Name:        payload.Artist.Name,
		Cover:       payload.Artist.PicURL,
		Description: payload.Artist.BriefDesc,
		SongCount:   payload.Artist.MusicSize,
		AlbumCount:  payload.Artist.AlbumSize,
		Songs:       p.mapTracks(tracks),
	}, nil
}

func (p *NeteaseProvider) Stream(id, quality string) (Stream, error) {
	if got, err := p.streamNative(id, quality); err == nil && got.URL != "" {
		return got, nil
	}
	// Fallback to music-lib (uses the QR-issued cookie) when the native path
	// can't resolve a URL — e.g. VIP track without a cookie configured natively.
	if got, err := streamViaMusicLib("netease", id, p.cookie()); err == nil && got.URL != "" {
		got.Quality = NormalizeQuality(quality)
		return got, nil
	}
	return Stream{}, ErrNotFound
}

func (p *NeteaseProvider) streamNative(id, quality string) (Stream, error) {
	level := NormalizeQuality(quality)
	encodeType := "flac"
	if level == QualityStandard || level == QualityExhigh {
		encodeType = "mp3"
	}

	body := fmt.Sprintf(`{"ids":"[%s]","level":"%s","encodeType":"%s","csrf_token":""}`, id, level, encodeType)
	params, encSecKey, err := weapiEncrypt(body)
	if err != nil {
		return Stream{}, err
	}
	form := url.Values{
		"params":    {params},
		"encSecKey": {encSecKey},
	}
	headers := p.headers()
	headers["Referer"] = "https://music.163.com/"
	headers["Origin"] = "https://music.163.com"

	data, err := httpPostForm(context.Background(), "https://music.163.com/weapi/song/enhance/player/url/v1", form, headers)
	if err != nil {
		return Stream{}, err
	}
	var payload struct {
		Code int `json:"code"`
		Data []struct {
			ID    int64  `json:"id"`
			URL   string `json:"url"`
			Size  int64  `json:"size"`
			BR    int    `json:"br"`
			Type  string `json:"type"`
			Level string `json:"level"`
			UF    struct {
				URL string `json:"url"`
			} `json:"uf"`
			FreeTrialInfo *struct{} `json:"freeTrialInfo"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return Stream{}, err
	}
	if len(payload.Data) == 0 {
		return Stream{}, ErrNotFound
	}
	first := payload.Data[0]
	streamURL := firstNonEmpty(first.URL, first.UF.URL)
	if streamURL == "" {
		return Stream{}, ErrNotFound
	}
	return Stream{
		URL:     normalizeURL(streamURL),
		Size:    first.Size,
		BR:      first.BR / 1000,
		Format:  first.Type,
		Quality: first.Level,
	}, nil
}

func (p *NeteaseProvider) Cover(id string, size int) (string, error) {
	tracks, err := p.songDetails([]string{id})
	if err != nil {
		return "", err
	}
	if len(tracks) == 0 {
		return "", ErrNotFound
	}
	coverURL := normalizeURL(tracks[0].AL.PicURL)
	if coverURL == "" {
		coverID := strconv.FormatInt(firstPositive(parseInt64(id), tracks[0].AL.Pic, parseInt64(tracks[0].AL.PicStr)), 10)
		if coverID == "" || coverID == "0" {
			return "", ErrNotFound
		}
		coverURL = fmt.Sprintf("https://p1.music.126.net/%s/%s.jpg", coverID, coverID)
	}
	if size > 0 {
		if strings.Contains(coverURL, "?") {
			coverURL += "&param=" + strconv.Itoa(size) + "y" + strconv.Itoa(size)
		} else {
			coverURL += "?param=" + strconv.Itoa(size) + "y" + strconv.Itoa(size)
		}
	}
	return coverURL, nil
}

func (p *NeteaseProvider) Lyric(id string) (Lyrics, error) {
	data, err := httpGet(context.Background(), "https://music.163.com/api/song/lyric", map[string]string{
		"id": id,
		"lv": "-1",
		"kv": "-1",
		"tv": "-1",
	}, p.headers())
	if err != nil {
		return Lyrics{}, err
	}
	var payload struct {
		LRC struct {
			Lyric string `json:"lyric"`
		} `json:"lrc"`
		TLRC struct {
			Lyric string `json:"lyric"`
		} `json:"tlyric"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return Lyrics{}, err
	}
	return Lyrics{
		Primary:    payload.LRC.Lyric,
		Translated: payload.TLRC.Lyric,
		Merged:     mergeLyrics(payload.LRC.Lyric, payload.TLRC.Lyric),
	}, nil
}

func (p *NeteaseProvider) songDetails(ids []string) ([]neteaseTrack, error) {
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, fmt.Sprintf(`{"id":%s,"v":0}`, id))
	}
	data, err := httpGet(context.Background(), "https://music.163.com/api/v3/song/detail", map[string]string{
		"c": "[" + strings.Join(values, ",") + "]",
	}, p.headers())
	if err != nil {
		return nil, err
	}
	var payload struct {
		Songs []neteaseTrack `json:"songs"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload.Songs, nil
}

func (p *NeteaseProvider) mapTracks(tracks []neteaseTrack) []Song {
	songs := make([]Song, 0, len(tracks))
	for _, track := range tracks {
		songs = append(songs, p.mapTrack(track))
	}
	return songs
}

func (p *NeteaseProvider) mapTrack(track neteaseTrack) Song {
	coverID := firstPositive(track.AL.Pic, parseInt64(track.AL.PicStr))
	return Song{
		ID:       strconv.FormatInt(track.ID, 10),
		Name:     track.Name,
		Artists:  collectNeteaseArtists(track.AR),
		Album:    track.AL.Name,
		Duration: int(track.Dt / 1000), // ms → s
		CoverID:  strconv.FormatInt(coverID, 10),
		StreamID: strconv.FormatInt(track.ID, 10),
		LyricID:  strconv.FormatInt(track.ID, 10),
		Provider: "netease",
	}
}

func parseInt64(raw string) int64 {
	value, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return value
}

func collectNeteaseArtists(items []struct {
	Name string `json:"name"`
}) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return sanitizeArtists(names)
}

func firstPositive(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
