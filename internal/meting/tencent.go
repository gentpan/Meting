package meting

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type TencentProvider struct {
	cfg    Config
	creds  Credentials
	signer *TencentSigner
}

func NewTencentProvider(cfg Config, creds Credentials) (*TencentProvider, error) {
	signer, err := newTencentSigner()
	if err != nil {
		return nil, err
	}
	return &TencentProvider{cfg: cfg, creds: creds, signer: signer}, nil
}

// cookie returns the live tencent/QQ cookie: credential store wins, env fallback.
func (p *TencentProvider) cookie() string {
	if p.creds != nil {
		if c := strings.TrimSpace(p.creds.Cookie("tencent")); c != "" {
			return c
		}
	}
	return p.cfg.TencentCookie
}

func (p *TencentProvider) Meta() ProviderDescriptor {
	return ProviderDescriptor{
		Name:        "tencent",
		DisplayName: "QQ 音乐",
		Status:      "partial",
		Resources:   resources(),
		Notes:       "QQ 音乐。搜索、单曲、歌单、专辑、歌手、歌词、封面均可用。",
	}
}

func (p *TencentProvider) headers() map[string]string {
	headers := map[string]string{
		"Accept":          "application/json, text/plain, */*",
		"Origin":          "https://y.qq.com",
		"Referer":         "https://y.qq.com/",
		"User-Agent":      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36",
		"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
	}
	if c := p.cookie(); c != "" {
		headers["Cookie"] = c
	}
	return headers
}

type tencentSong struct {
	Mid      string `json:"mid"`
	Name     string `json:"name"`
	Title    string `json:"title"`
	Type     int    `json:"type"`
	Interval int    `json:"interval"` // 时长（秒）
	Singer   []struct {
		Name string `json:"name"`
	} `json:"singer"`
	Album struct {
		ID    int64  `json:"id"`
		Mid   string `json:"mid"`
		Name  string `json:"name"`
		Title string `json:"title"`
	} `json:"album"`
	File struct {
		MediaMid   string  `json:"media_mid"`
		SizeFlac   int64   `json:"size_flac"`
		Size320mp3 int64   `json:"size_320mp3"`
		Size192aac int64   `json:"size_192aac"`
		Size128mp3 int64   `json:"size_128mp3"`
		Size96aac  int64   `json:"size_96aac"`
		Size48aac  int64   `json:"size_48aac"`
		Size24aac  int64   `json:"size_24aac"`
		SizeHires  int64   `json:"size_hires"`
		SizeDolby  int64   `json:"size_dolby"`
		SizeNew    []int64 `json:"size_new"`
	} `json:"file"`
	MusicData *tencentSong `json:"musicData"`
}

func (p *TencentProvider) Search(keyword string, page, limit int) ([]Song, error) {
	if strings.TrimSpace(keyword) == "" {
		return nil, nil
	}
	// QQ official search rejects num_per_page > 50 silently. Clamp.
	if limit > 50 {
		limit = 50
	}
	songs, err := p.searchOfficial(keyword, page, limit)
	if err == nil && len(songs) > 0 {
		return songs, nil
	}
	fallback, fallbackErr := p.searchSmartbox(keyword, limit)
	if fallbackErr != nil {
		if err != nil {
			return nil, err
		}
		return nil, fallbackErr
	}
	return fallback, nil
}

func (p *TencentProvider) Song(id string) (Song, error) {
	song, err := p.fetchSong(id)
	if err != nil {
		return Song{}, err
	}
	return p.mapSong(song), nil
}

func (p *TencentProvider) Playlist(id string) ([]Song, error) {
	data, err := httpGet(context.Background(), "https://c.y.qq.com/v8/fcg-bin/fcg_v8_playlist_cp.fcg", map[string]string{
		"id":       id,
		"format":   "json",
		"newsong":  "1",
		"platform": "jqspaframe.json",
	}, p.headers())
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data struct {
			CDList []struct {
				SongList []tencentSong `json:"songlist"`
			} `json:"cdlist"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if len(payload.Data.CDList) == 0 {
		return nil, ErrNotFound
	}
	return p.mapSongs(payload.Data.CDList[0].SongList), nil
}

func (p *TencentProvider) Album(id string) ([]Song, error) {
	detail, err := p.AlbumDetail(id)
	if err != nil {
		return nil, err
	}
	return detail.Songs, nil
}

func (p *TencentProvider) AlbumDetail(id string) (AlbumDetail, error) {
	data, err := httpGet(context.Background(), "https://c.y.qq.com/v8/fcg-bin/fcg_v8_album_detail_cp.fcg", map[string]string{
		"albummid": id,
		"platform": "mac",
		"format":   "json",
		"newsong":  "1",
	}, p.headers())
	if err != nil {
		return AlbumDetail{}, err
	}
	var payload struct {
		Data struct {
			GetAlbumInfo struct {
				FalbumName string `json:"Falbum_name"`
				FalbumMid  string `json:"Falbum_mid"`
			} `json:"getAlbumInfo"`
			GetAlbumDesc struct {
				FalbumDesc string `json:"Falbum_desc"`
			} `json:"getAlbumDesc"`
			GetCompanyInfo struct {
				FcompanyName string `json:"Fcompany_name"`
			} `json:"getCompanyInfo"`
			GetSongInfo []tencentSong `json:"getSongInfo"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return AlbumDetail{}, err
	}
	songs := p.mapSongs(payload.Data.GetSongInfo)
	var artist string
	var artists []string
	if len(songs) > 0 {
		artist = strings.Join(songs[0].Artists, " / ")
		artists = append([]string(nil), songs[0].Artists...)
	}
	cover := ""
	if mid := payload.Data.GetAlbumInfo.FalbumMid; mid != "" {
		cover = fmt.Sprintf("https://y.gtimg.cn/music/photo_new/T002R500x500M000%s.jpg", mid)
	}
	return AlbumDetail{
		ID:          id,
		Name:        payload.Data.GetAlbumInfo.FalbumName,
		Cover:       cover,
		Artist:      artist,
		Artists:     artists,
		Description: strings.TrimSpace(payload.Data.GetAlbumDesc.FalbumDesc),
		Company:     payload.Data.GetCompanyInfo.FcompanyName,
		Songs:       songs,
	}, nil
}

func (p *TencentProvider) Artist(id string, limit int) ([]Song, error) {
	detail, err := p.ArtistDetail(id, limit)
	if err != nil {
		return nil, err
	}
	return detail.Songs, nil
}

func (p *TencentProvider) ArtistDetail(id string, limit int) (ArtistDetail, error) {
	// QQ 弃用了 fcg_v8_singer_track_cp.fcg (404)；改用 musicu.fcg + web_singer_info_svr。
	reqBody := fmt.Sprintf(
		`{"r":{"module":"music.web_singer_info_svr","method":"get_singer_detail_info","param":{"sort":5,"singermid":"%s","sin":0,"num":%d}}}`,
		id, limit,
	)
	data, err := httpGet(context.Background(), "https://u.y.qq.com/cgi-bin/musicu.fcg", map[string]string{
		"format": "json",
		"data":   reqBody,
	}, p.headers())
	if err != nil {
		return ArtistDetail{}, err
	}
	var payload struct {
		R struct {
			Data struct {
				Songlist []tencentSong `json:"songlist"`
			} `json:"data"`
		} `json:"r"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return ArtistDetail{}, err
	}
	songs := p.mapSongs(payload.R.Data.Songlist)
	name := ""
	if len(songs) > 0 && len(songs[0].Artists) > 0 {
		name = songs[0].Artists[0]
	}
	cover := fmt.Sprintf("https://y.gtimg.cn/music/photo_new/T001R500x500M000%s.jpg", id)
	return ArtistDetail{
		ID:    id,
		Name:  name,
		Cover: cover,
		Songs: songs,
	}, nil
}

func (p *TencentProvider) Stream(id, quality string) (Stream, error) {
	got, nerr := p.streamNative(id, quality)
	if nerr == nil && got.URL != "" {
		return got, nil
	}
	if got2, err := streamViaMusicLib("tencent", id, p.cookie()); err == nil && got2.URL != "" {
		got2.Quality = NormalizeQuality(quality)
		return got2, nil
	}
	if nerr != nil {
		return Stream{}, nerr // surface the native failure (e.g. vkey code) instead of a bare not-found
	}
	return Stream{}, ErrNotFound
}

// normalizeQQUin turns a cookie uin like "o0418xxxxx" into the bare numeric QQ
// number the vkey API expects (strip a leading o/O and zero-padding).
func normalizeQQUin(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "o")
	raw = strings.TrimPrefix(raw, "O")
	raw = strings.TrimLeft(raw, "0")
	return raw
}

func (p *TencentProvider) streamNative(id, quality string) (Stream, error) {
	song, err := p.fetchSong(id)
	if err != nil {
		return Stream{}, err
	}
	candidate := p.normalizeSong(song)
	type streamCandidate struct {
		Quality string
		Size    int64
		BR      int
		Prefix  string
		Ext     string
	}
	hiresSize := candidate.File.SizeHires
	dolbySize := candidate.File.SizeDolby
	if hiresSize == 0 && len(candidate.File.SizeNew) > 0 {
		// size_new[5] often holds the hires size on QQ. Use first non-zero entry as fallback.
		for _, v := range candidate.File.SizeNew {
			if v > 0 {
				hiresSize = v
				break
			}
		}
	}
	allCandidates := []streamCandidate{
		{QualityJymaster, hiresSize, 1999, "AI00", "flac"},
		{QualityDolby, dolbySize, 1500, "O801", "m4a"},
		{QualityHires, hiresSize, 1999, "RS01", "flac"},
		{QualityLossless, candidate.File.SizeFlac, 999, "F000", "flac"},
		{QualityExhigh, candidate.File.Size320mp3, 320, "M800", "mp3"},
		{"aac192", candidate.File.Size192aac, 192, "C600", "m4a"},
		{QualityStandard, candidate.File.Size128mp3, 128, "M500", "mp3"},
		{"aac96", candidate.File.Size96aac, 96, "C400", "m4a"},
		{"aac48", candidate.File.Size48aac, 48, "C200", "m4a"},
		{"aac24", candidate.File.Size24aac, 24, "C100", "m4a"},
	}
	requested := NormalizeQuality(quality)
	startIdx := 0
	for i, c := range allCandidates {
		if c.Quality == requested {
			startIdx = i
			break
		}
	}
	candidates := allCandidates[startIdx:]

	uin := "0"
	if value := normalizeQQUin(parseCookieString(p.cookie())["uin"]); value != "" {
		uin = value
	}

	payload := map[string]any{
		"req_0": map[string]any{
			"module": "vkey.GetVkeyServer",
			"method": "CgiGetVkey",
			"param": map[string]any{
				"guid":      strconv.FormatInt(time.Now().UnixNano()%10000000000, 10),
				"songmid":   []string{},
				"filename":  []string{},
				"songtype":  []int{},
				"uin":       uin,
				"loginflag": 1,
				"platform":  "20",
			},
		},
	}
	param := payload["req_0"].(map[string]any)["param"].(map[string]any)
	for _, item := range candidates {
		param["songmid"] = append(param["songmid"].([]string), candidate.Mid)
		param["filename"] = append(param["filename"].([]string), item.Prefix+candidate.File.MediaMid+"."+item.Ext)
		param["songtype"] = append(param["songtype"].([]int), candidate.Type)
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return Stream{}, err
	}
	// QQ enforces a signed request on the vkey endpoint (same as official search).
	// An unsigned GET returns empty purl for every tier, so sign + POST to u6.
	sign, err := p.signer.Sign(string(raw))
	if err != nil {
		return Stream{}, err
	}
	data, err := httpPostRaw(context.Background(), "https://u6.y.qq.com/cgi-bin/musicu.fcg?sign="+url.QueryEscape(sign), raw, p.headers())
	if err != nil {
		return Stream{}, err
	}
	var response struct {
		Req0 struct {
			Code int `json:"code"`
			Data struct {
				SIP        []string `json:"sip"`
				MidURLInfo []struct {
					PURL string `json:"purl"`
				} `json:"midurlinfo"`
			} `json:"data"`
		} `json:"req_0"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return Stream{}, err
	}
	if len(response.Req0.Data.SIP) == 0 {
		return Stream{}, fmt.Errorf("qq vkey: no sip (code=%d, urlinfos=%d)", response.Req0.Code, len(response.Req0.Data.MidURLInfo))
	}
	purlCount, sizeCount := 0, 0
	for idx, item := range candidates {
		if idx >= len(response.Req0.Data.MidURLInfo) {
			break
		}
		purl := response.Req0.Data.MidURLInfo[idx].PURL
		if purl != "" {
			purlCount++
		}
		if item.Size > 0 {
			sizeCount++
		}
		// Skip tiers the song lacks (size_xxx == 0) or that vkey refused (empty purl).
		// QQ's vkey server sometimes returns a non-empty PURL for tiers the account
		// can't actually access — without this guard we'd hand out a broken URL.
		if purl == "" || item.Size <= 0 {
			continue
		}
		return Stream{
			URL:     normalizeURL(response.Req0.Data.SIP[0] + purl),
			Size:    item.Size,
			BR:      item.BR,
			Format:  item.Ext,
			Quality: item.Quality,
		}, nil
	}
	return Stream{}, fmt.Errorf("qq vkey: no playable tier (sip=%d purls=%d/%d sized=%d/%d)",
		len(response.Req0.Data.SIP), purlCount, len(candidates), sizeCount, len(candidates))
}

func (p *TencentProvider) Cover(id string, size int) (string, error) {
	song, err := p.fetchSong(id)
	if err != nil {
		return "", err
	}
	normalized := p.normalizeSong(song)
	if normalized.Album.Mid == "" {
		return "", ErrNotFound
	}
	chosen := tencentCoverSize(size)
	// 800 is only present when the album's source art is >= 800px. Probe once
	// and fall back to the universally-available 500 if it's missing.
	if chosen == 800 && !tencentCoverExists(normalized.Album.Mid, 800) {
		chosen = 500
	}
	return fmt.Sprintf("https://y.gtimg.cn/music/photo_new/T002R%dx%dM000%s.jpg?max_age=2592000", chosen, chosen, normalized.Album.Mid), nil
}

// tencentCoverSize quantises an arbitrary requested size to the four tiers
// y.gtimg.cn/photo_new actually generates. Any other value 404s.
func tencentCoverSize(want int) int {
	switch {
	case want <= 0:
		return 500
	case want <= 150:
		return 150
	case want <= 300:
		return 300
	case want <= 500:
		return 500
	default:
		return 800
	}
}

// tencentCoverExists HEADs the cover URL with a short timeout so a slow
// upstream can't stall the /cover endpoint.
func tencentCoverExists(mid string, size int) bool {
	probeURL := fmt.Sprintf("https://y.gtimg.cn/music/photo_new/T002R%dx%dM000%s.jpg", size, size, mid)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, status, _ := doRequest(ctx, http.MethodHead, probeURL, nil, nil)
	return status == http.StatusOK
}

func (p *TencentProvider) Lyric(id string) (Lyrics, error) {
	data, err := httpGet(context.Background(), "https://c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg", map[string]string{
		"songmid":  id,
		"g_tk":     "5381",
		"format":   "json",
		"nobase64": "0",
	}, p.headers())
	if err != nil {
		return Lyrics{}, err
	}
	var payload struct {
		Lyric string `json:"lyric"`
		Trans string `json:"trans"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return Lyrics{}, err
	}
	primary := decodeTencentBase64(payload.Lyric)
	translated := decodeTencentBase64(payload.Trans)
	return Lyrics{
		Primary:    primary,
		Translated: translated,
		Merged:     mergeLyrics(primary, translated),
	}, nil
}

func (p *TencentProvider) searchOfficial(keyword string, page, limit int) ([]Song, error) {
	payload := map[string]any{
		"req_1": map[string]any{
			"module": "music.search.SearchCgiService",
			"method": "DoSearchForQQMusicDesktop",
			"param": map[string]any{
				"remoteplace":  "txt.mqq.all",
				"query":        keyword,
				"search_type":  0,
				"num_per_page": limit,
				"page_num":     page,
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	sign, err := p.signer.Sign(string(body))
	if err != nil {
		return nil, err
	}
	data, err := httpPostRaw(context.Background(), "https://u6.y.qq.com/cgi-bin/musicu.fcg?sign="+url.QueryEscape(sign), body, p.headers())
	if err != nil {
		return nil, err
	}
	var response struct {
		Req1 struct {
			Code int `json:"code"`
			Data struct {
				Body struct {
					Song struct {
						List []tencentSong `json:"list"`
					} `json:"song"`
				} `json:"body"`
			} `json:"data"`
		} `json:"req_1"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	return p.mapSongs(response.Req1.Data.Body.Song.List), nil
}

func (p *TencentProvider) searchSmartbox(keyword string, limit int) ([]Song, error) {
	data, err := httpGet(context.Background(), "https://c6.y.qq.com/splcloud/fcgi-bin/smartbox_new.fcg", map[string]string{
		"format":   "json",
		"platform": "yqq.json",
		"key":      keyword,
	}, p.headers())
	if err != nil {
		return nil, err
	}
	var response struct {
		Data struct {
			Song struct {
				ItemList []struct {
					Mid    string `json:"mid"`
					Name   string `json:"name"`
					Singer string `json:"singer"`
					Album  string `json:"album"`
				} `json:"itemlist"`
			} `json:"song"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	songs := make([]Song, 0, len(response.Data.Song.ItemList))
	for _, item := range response.Data.Song.ItemList {
		if item.Mid == "" {
			continue
		}
		songs = append(songs, Song{
			ID:       item.Mid,
			Name:     item.Name,
			Artists:  sanitizeArtists(strings.Split(item.Singer, "/")),
			Album:    item.Album,
			StreamID: item.Mid,
			LyricID:  item.Mid,
			Provider: "tencent",
		})
		if limit > 0 && len(songs) >= limit {
			break
		}
	}
	return songs, nil
}

func (p *TencentProvider) fetchSong(id string) (tencentSong, error) {
	data, err := httpGet(context.Background(), "https://c.y.qq.com/v8/fcg-bin/fcg_play_single_song.fcg", map[string]string{
		"songmid":  id,
		"platform": "yqq",
		"format":   "json",
	}, p.headers())
	if err != nil {
		return tencentSong{}, err
	}
	var payload struct {
		Data []tencentSong `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return tencentSong{}, err
	}
	if len(payload.Data) == 0 {
		return tencentSong{}, ErrNotFound
	}
	return p.normalizeSong(payload.Data[0]), nil
}

func (p *TencentProvider) mapSongs(items []tencentSong) []Song {
	songs := make([]Song, 0, len(items))
	for _, item := range items {
		songs = append(songs, p.mapSong(item))
	}
	return songs
}

func (p *TencentProvider) mapSong(item tencentSong) Song {
	normalized := p.normalizeSong(item)
	album := firstNonEmpty(normalized.Album.Title, normalized.Album.Name)
	return Song{
		ID:       normalized.Mid,
		Name:     firstNonEmpty(normalized.Name, normalized.Title),
		Artists:  sanitizeArtists(collectTencentArtists(normalized.Singer)),
		Album:    album,
		Duration: normalized.Interval,
		CoverID:  normalized.Album.Mid,
		StreamID: normalized.Mid,
		LyricID:  normalized.Mid,
		Provider: "tencent",
	}
}

func (p *TencentProvider) normalizeSong(item tencentSong) tencentSong {
	if item.MusicData != nil {
		return *item.MusicData
	}
	return item
}

func collectTencentArtists(items []struct {
	Name string `json:"name"`
}) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names
}

func decodeTencentBase64(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return ""
	}
	return string(decoded)
}
