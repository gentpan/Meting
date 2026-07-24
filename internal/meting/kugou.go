package meting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	mlkugou "github.com/guohuiyuan/music-lib/kugou"
	mlmodel "github.com/guohuiyuan/music-lib/model"
)

type KugouProvider struct {
	cfg   Config
	creds Credentials
}

func NewKugouProvider(cfg Config, creds Credentials) *KugouProvider {
	return &KugouProvider{cfg: cfg, creds: creds}
}

// cookie returns the live kugou cookie: the credential store wins (set via the
// admin QR login), falling back to the static KUGOU_COOKIE env value.
func (p *KugouProvider) cookie() string {
	if p.creds != nil {
		if c := strings.TrimSpace(p.creds.Cookie("kugou")); c != "" {
			return c
		}
	}
	return p.cfg.KugouCookie
}

// streamViaLib resolves a direct URL through music-lib using the QR-issued app
// token. music-lib's GetDownloadURL only takes the VIP path when the song
// carries privilege==10 and an album_audio_id (normally filled by its own
// search). Coming from meting we only have the hash, so we resolve the
// album_audio_id ourselves (get_res_privilege) and force the VIP branch.
func (p *KugouProvider) streamViaLib(hash string) (Stream, error) {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return Stream{}, ErrNotFound
	}
	extra := map[string]string{"hash": hash, "privilege": "10"}
	// album_audio_id is required for the VIP (flac) path; get_res_privilege is
	// occasionally flaky, so retry a few times before falling back to 0 (which
	// would force the free 128k path).
	for i := 0; i < 3; i++ {
		if aaid, err := p.kugouAlbumAudioID(hash); err == nil && aaid > 0 {
			extra["album_audio_id"] = strconv.FormatInt(aaid, 10)
			break
		}
	}
	song := &mlmodel.Song{Source: "kugou", ID: hash, Extra: extra}
	rawURL, err := mlkugou.New(p.cookie()).GetDownloadURL(song)
	if err != nil {
		return Stream{}, err
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return Stream{}, ErrNotFound
	}
	return Stream{URL: rawURL, Format: extFromURL(rawURL)}, nil
}

func (p *KugouProvider) Meta() ProviderDescriptor {
	return ProviderDescriptor{
		Name:        "kugou",
		DisplayName: "酷狗音乐",
		Status:      "partial",
		Resources:   resources(),
		Notes:       "酷狗音乐。搜索、单曲、歌单、专辑、歌手、歌词、封面均可用。",
	}
}

func (p *KugouProvider) headers() map[string]string {
	headers := map[string]string{
		"Accept":          "application/json, text/plain, */*",
		"User-Agent":      "IPhone-8990-searchSong",
		"UNI-UserAgent":   "iOS11.4-Phone8990-1009-0-WiFi",
		"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
	}
	if p.cookie() != "" {
		headers["Cookie"] = p.cookie()
	}
	return headers
}

type kugouTrack struct {
	Hash       string `json:"hash"`
	Filename   string `json:"filename"`
	FileName   string `json:"fileName"`
	SongName   string `json:"songname"`
	SingerName string `json:"singername"`
	AlbumName  string `json:"album_name"`
	Duration   int    `json:"duration"` // 时长（秒）
	TimeLength int    `json:"timelen"`  // album/singer 接口里用 timelen 名字
	ImgURL     string `json:"imgUrl"`
}

func (p *KugouProvider) Search(keyword string, page, limit int) ([]Song, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return []Song{}, nil
	}
	// Kugou mobilecdn caps pagesize at 30 silently.
	if limit > 30 {
		limit = 30
	}
	data, err := httpGet(context.Background(), "http://mobilecdn.kugou.com/api/v3/search/song", map[string]string{
		"api_ver":   "1",
		"area_code": "1",
		"correct":   "1",
		"pagesize":  strconv.Itoa(limit),
		"plat":      "2",
		"tag":       "1",
		"sver":      "5",
		"showtype":  "10",
		"page":      strconv.Itoa(page),
		"keyword":   keyword,
		"version":   "8990",
	}, p.headers())
	if err != nil {
		return nil, err
	}
	items, err := parseKugouSearchInfo(data)
	if err != nil {
		return nil, err
	}
	return p.mapTracks(items), nil
}

func parseKugouSearchInfo(data []byte) ([]kugouTrack, error) {
	var payload struct {
		Data struct {
			Info json.RawMessage `json:"info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	raw := bytes.TrimSpace(payload.Data.Info)
	if len(raw) == 0 || string(raw) == `""` || string(raw) == "null" {
		return []kugouTrack{}, nil
	}
	if raw[0] == '"' {
		return []kugouTrack{}, nil
	}
	var items []kugouTrack
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (p *KugouProvider) Song(id string) (Song, error) {
	track, err := p.fetchTrack(id)
	if err != nil {
		return Song{}, err
	}
	return p.mapTrack(track), nil
}

func (p *KugouProvider) Playlist(id string) ([]Song, error) {
	data, err := httpGet(context.Background(), "http://mobilecdn.kugou.com/api/v3/special/song", map[string]string{
		"specialid": id,
		"area_code": "1",
		"page":      "1",
		"plat":      "2",
		"pagesize":  "-1",
		"version":   "8990",
	}, p.headers())
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data struct {
			Info []kugouTrack `json:"info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return p.mapTracks(payload.Data.Info), nil
}

func (p *KugouProvider) Album(id string) ([]Song, error) {
	detail, err := p.AlbumDetail(id)
	if err != nil {
		return nil, err
	}
	return detail.Songs, nil
}

func (p *KugouProvider) AlbumDetail(id string) (AlbumDetail, error) {
	// 1) songs list
	songData, err := httpGet(context.Background(), "http://mobilecdn.kugou.com/api/v3/album/song", map[string]string{
		"albumid":   id,
		"area_code": "1",
		"plat":      "2",
		"page":      "1",
		"pagesize":  "-1",
		"version":   "8990",
	}, p.headers())
	if err != nil {
		return AlbumDetail{}, err
	}
	var songsPayload struct {
		Data struct {
			Info []kugouTrack `json:"info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(songData, &songsPayload); err != nil {
		return AlbumDetail{}, err
	}
	songs := p.mapTracks(songsPayload.Data.Info)

	// 2) album metadata (best-effort; failure here keeps songs but drops metadata)
	infoData, err := httpGet(context.Background(), "http://mobilecdn.kugou.com/api/v3/album/info", map[string]string{
		"albumid": id,
		"plat":    "2",
	}, p.headers())
	if err != nil {
		return AlbumDetail{ID: id, Songs: songs}, nil
	}
	var infoPayload struct {
		Data struct {
			AlbumName   string `json:"albumname"`
			SingerName  string `json:"singername"`
			PublishTime string `json:"publishtime"`
			ImgURL      string `json:"imgurl"`
			Intro       string `json:"intro"`
		} `json:"data"`
	}
	if err := json.Unmarshal(infoData, &infoPayload); err != nil {
		return AlbumDetail{ID: id, Songs: songs}, nil
	}
	d := infoPayload.Data
	cover := strings.ReplaceAll(d.ImgURL, "{size}", "480")
	publish := d.PublishTime
	if len(publish) >= 10 {
		publish = publish[:10] // strip time, keep "YYYY-MM-DD"
	}
	artists := []string{}
	if d.SingerName != "" {
		artists = strings.Split(d.SingerName, "、")
	}
	return AlbumDetail{
		ID:          id,
		Name:        d.AlbumName,
		Cover:       cover,
		Artist:      d.SingerName,
		Artists:     artists,
		Publish:     publish,
		Description: strings.TrimSpace(d.Intro),
		Songs:       songs,
	}, nil
}

func (p *KugouProvider) Artist(id string, limit int) ([]Song, error) {
	detail, err := p.ArtistDetail(id, limit)
	if err != nil {
		return nil, err
	}
	return detail.Songs, nil
}

func (p *KugouProvider) ArtistDetail(id string, limit int) (ArtistDetail, error) {
	songData, err := httpGet(context.Background(), "http://mobilecdn.kugou.com/api/v3/singer/song", map[string]string{
		"singerid":  id,
		"area_code": "1",
		"page":      "1",
		"plat":      "0",
		"pagesize":  strconv.Itoa(limit),
		"version":   "8990",
	}, p.headers())
	if err != nil {
		return ArtistDetail{}, err
	}
	var songsPayload struct {
		Data struct {
			Info []kugouTrack `json:"info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(songData, &songsPayload); err != nil {
		return ArtistDetail{}, err
	}
	songs := p.mapTracks(songsPayload.Data.Info)

	infoData, err := httpGet(context.Background(), "http://mobilecdn.kugou.com/api/v3/singer/info", map[string]string{
		"singerid": id,
		"plat":     "2",
	}, p.headers())
	if err != nil {
		return ArtistDetail{ID: id, Songs: songs}, nil
	}
	var infoPayload struct {
		Data struct {
			SingerName string `json:"singername"`
			ImgURL     string `json:"imgurl"`
			Intro      string `json:"intro"`
			SongCount  int    `json:"songcount"`
			AlbumCount int    `json:"albumcount"`
		} `json:"data"`
	}
	if err := json.Unmarshal(infoData, &infoPayload); err != nil {
		return ArtistDetail{ID: id, Songs: songs}, nil
	}
	d := infoPayload.Data
	return ArtistDetail{
		ID:          id,
		Name:        d.SingerName,
		Cover:       strings.ReplaceAll(d.ImgURL, "{size}", "480"),
		Description: strings.TrimSpace(d.Intro),
		SongCount:   d.SongCount,
		AlbumCount:  d.AlbumCount,
		Songs:       songs,
	}, nil
}

func (p *KugouProvider) Stream(id, quality string) (Stream, error) {
	// music-lib carries the Lite(3116) signed flow + QR-issued app token — the
	// only path that reliably yields VIP 直链. Try it first when a cookie exists.
	if p.cookie() != "" {
		if got, err := p.streamViaLib(id); err == nil && got.URL != "" {
			got.Quality = NormalizeQuality(quality)
			return got, nil
		}
	}
	// Legacy meting tiers as fallback (free tracks / no cookie).
	if got, err := p.streamPrivV6(id, quality); err == nil && got.URL != "" {
		return got, nil
	}
	if got, err := p.streamSigned(id, quality); err == nil && got.URL != "" {
		return got, nil
	}
	if p.cookie() != "" {
		if got, err := p.streamWeb(id, quality); err == nil && got.URL != "" {
			return got, nil
		}
	}
	return p.streamLegacy(id)
}

func (p *KugouProvider) webHeaders() map[string]string {
	headers := map[string]string{
		"Accept":          "application/json, text/plain, */*",
		"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36",
		"Referer":         "https://www.kugou.com/",
		"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
	}
	if p.cookie() != "" {
		headers["Cookie"] = p.cookie()
	}
	return headers
}

// streamWeb uses the browser play/songinfo endpoint (webfs.kugou.com URLs).
func (p *KugouProvider) streamWeb(id, quality string) (Stream, error) {
	encodeID, err := p.kugouEMixSongID(id)
	if err != nil || encodeID == "" {
		return Stream{}, ErrNotFound
	}
	cookies := parseKugouCookie(p.cookie())
	userID := cookies["KugooID"]
	if userID == "" {
		userID = "0"
	}
	mid := cookies["kg_mid"]
	if mid == "" {
		mid = cookies["mid"]
	}
	if mid == "" {
		return Stream{}, ErrNotFound
	}
	dfid := cookies["kg_dfid"]
	if dfid == "" {
		dfid = cookies["dfid"]
	}
	if dfid == "" {
		dfid = "-"
	}
	token := cookies["t"]
	clienttime := fmt.Sprintf("%d", time.Now().UnixMilli())

	params := map[string]string{
		"srcappid":              kgWebSrcAppID,
		"clientver":             kgWebClientver,
		"clienttime":            clienttime,
		"mid":                   mid,
		"uuid":                  mid,
		"dfid":                  dfid,
		"appid":                 kgWebAppID,
		"platid":                "4",
		"encode_album_audio_id": encodeID,
		"token":                 token,
		"userid":                userID,
	}
	params["signature"] = kgSignWeb(params)

	data, err := httpGet(context.Background(), "https://wwwapi.kugou.com/play/songinfo", params, p.webHeaders())
	if err != nil {
		return Stream{}, err
	}
	var resp struct {
		Status    int `json:"status"`
		ErrorCode int `json:"err_code"`
		Data      struct {
			PlayURL    string `json:"play_url"`
			FileSize   int64  `json:"filesize"`
			TimeLength int    `json:"timelength"`
			Bitrate    int    `json:"bitrate"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return Stream{}, err
	}
	if resp.Status != 1 || resp.Data.PlayURL == "" {
		return Stream{}, ErrNotFound
	}
	br := resp.Data.Bitrate
	if br == 0 && resp.Data.TimeLength > 0 && resp.Data.FileSize > 0 {
		br = int(resp.Data.FileSize * 8 / int64(resp.Data.TimeLength))
	}
	if br == 0 {
		br = 128
	}
	return Stream{
		URL:     resp.Data.PlayURL,
		Size:    resp.Data.FileSize,
		BR:      br,
		Format:  "mp3",
		Quality: QualityStandard,
	}, nil
}

func (p *KugouProvider) kugouEMixSongID(hash string) (string, error) {
	track, err := p.fetchTrack(hash)
	if err != nil {
		return "", err
	}
	keyword := firstNonEmpty(track.Filename, track.FileName)
	if keyword == "" {
		return "", fmt.Errorf("kugou: empty search keyword")
	}

	cookies := parseKugouCookie(p.cookie())
	userID := cookies["KugooID"]
	if userID == "" {
		userID = "0"
	}
	mid := cookies["kg_mid"]
	if mid == "" {
		mid = cookies["mid"]
	}
	if mid == "" {
		return "", fmt.Errorf("kugou: missing mid")
	}
	dfid := cookies["kg_dfid"]
	if dfid == "" {
		dfid = cookies["dfid"]
	}
	if dfid == "" {
		dfid = "-"
	}
	token := cookies["t"]
	clienttime := fmt.Sprintf("%d", time.Now().UnixMilli())
	wantHash := strings.ToUpper(hash)

	params := map[string]string{
		"callback":         "callback123",
		"srcappid":         kgWebSrcAppID,
		"clientver":        kgWebClientver,
		"clienttime":       clienttime,
		"mid":              mid,
		"uuid":             mid,
		"dfid":             dfid,
		"keyword":          keyword,
		"page":             "1",
		"pagesize":         "30",
		"bitrate":          "0",
		"isfuzzy":          "0",
		"inputtype":        "0",
		"platform":         "WebFilter",
		"userid":           userID,
		"iscorrection":     "1",
		"privilege_filter": "0",
		"filter":           "10",
		"token":            token,
		"appid":            kgWebAppID,
	}
	params["signature"] = kgSignWeb(params)

	data, err := httpGet(context.Background(), "https://complexsearch.kugou.com/v2/search/song", params, p.webHeaders())
	if err != nil {
		return "", err
	}
	payload, err := unwrapJSONP("callback123", data)
	if err != nil {
		return "", err
	}
	var search struct {
		Data struct {
			Lists []struct {
				FileHash   string `json:"FileHash"`
				EMixSongID string `json:"EMixSongID"`
			} `json:"lists"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &search); err != nil {
		return "", err
	}
	for _, item := range search.Data.Lists {
		if strings.EqualFold(item.FileHash, wantHash) && item.EMixSongID != "" {
			return item.EMixSongID, nil
		}
	}
	if len(search.Data.Lists) > 0 && search.Data.Lists[0].EMixSongID != "" {
		return search.Data.Lists[0].EMixSongID, nil
	}
	return "", fmt.Errorf("kugou: EMixSongID not found")
}

func unwrapJSONP(callback string, data []byte) ([]byte, error) {
	text := strings.TrimSpace(string(data))
	prefix := callback + "("
	if !strings.HasPrefix(text, prefix) || !strings.HasSuffix(text, ")") {
		return data, nil
	}
	return []byte(text[len(prefix) : len(text)-1]), nil
}

// streamSigned uses kugou's modern signed gateway/v5/url endpoint, which honours
// VIP entitlement when the session cookie carries the right tokens.
func (p *KugouProvider) streamSigned(id, quality string) (Stream, error) {
	cookies := parseKugouCookie(p.cookie())
	userID := cookies["KugooID"]
	if userID == "" {
		userID = "0"
	}
	mid := cookies["kg_mid"]
	dfid := cookies["kg_dfid"]
	if dfid == "" {
		dfid = "-"
	}
	token := cookies["t"]
	if mid == "" {
		return Stream{}, ErrNotFound
	}

	q := kgMapQuality(quality)
	now := time.Now().Unix()
	clienttime := kgClienttime(now)
	hashLower := strings.ToLower(id)
	albumAudioID := "0"
	if aaid, err := p.kugouAlbumAudioID(id); err == nil && aaid > 0 {
		albumAudioID = strconv.FormatInt(aaid, 10)
	}

	params := map[string]string{
		"album_id":       "0",
		"area_code":      "1",
		"hash":           hashLower,
		"ssa_flag":       "is_fromtrack",
		"version":        kgClientver,
		"page_id":        "151369488",
		"quality":        q,
		"album_audio_id": albumAudioID,
		"behavior":       "play",
		"pid":            "2",
		"cmd":            "26",
		"pidversion":     "3001",
		"IsFreePart":     "0",
		"ppage_id":       "463467626,350369493,788954147",
		"cdnBackup":      "1",
		"module":         "",
		"clientver":      kgClientver,
		"appid":          kgAppID,
		"clienttime":     clienttime,
		"dfid":           dfid,
		"mid":            mid,
		"uuid":           "-",
		"userid":         userID,
		"token":          token,
	}
	params["key"] = kgSignKey(hashLower, mid, userID)
	params["signature"] = kgSignAndroid(params, "")

	headers := map[string]string{
		"User-Agent": kgUserAgent(),
		"x-router":   "trackercdn.kugou.com",
		"dfid":       dfid,
		"clienttime": clienttime,
		"mid":        mid,
		"kg-rc":      "1",
		"kg-thash":   "5d816a0",
		"kg-rec":     "1",
		"kg-rf":      "B9EDA08A64250DEFFBCADDEE00F8F25F",
		"Cookie":     p.cookie(),
	}

	data, err := httpGet(context.Background(), "https://gateway.kugou.com/v5/url", params, headers)
	if err != nil {
		return Stream{}, err
	}
	var resp struct {
		Status    int      `json:"status"`
		URL       []string `json:"url"`
		BackupURL []string `json:"backupUrl"`
		FileSize  int64    `json:"fileSize"`
		BitRate   int      `json:"bitRate"`
		ExtName   string   `json:"extName"`
		ErrorCode int      `json:"error_code"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return Stream{}, err
	}
	if resp.Status != 1 || len(resp.URL) == 0 {
		return Stream{}, ErrNotFound
	}
	picked := resp.URL[0]
	if picked == "" && len(resp.BackupURL) > 0 {
		picked = resp.BackupURL[0]
	}
	if picked == "" {
		return Stream{}, ErrNotFound
	}
	return Stream{
		URL:     picked, // keep http:// — kugou CDN does not present valid HTTPS cert
		Size:    resp.FileSize,
		BR:      resp.BitRate / 1000,
		Format:  resp.ExtName,
		Quality: NormalizeQuality(quality),
	}, nil
}

// streamPrivV6 uses the lite tracker endpoint that honours VIP entitlement.
// It requires vip_token and vip_type in KUGOU_COOKIE (issued after login refresh).
func (p *KugouProvider) streamPrivV6(id, quality string) (Stream, error) {
	cookies := parseKugouCookie(p.cookie())
	vipToken := cookies["vip_token"]
	if vipToken == "" {
		return Stream{}, ErrNotFound
	}
	userID := cookies["KugooID"]
	if userID == "" {
		userID = "0"
	}
	mid := cookies["kg_mid"]
	dfid := cookies["kg_dfid"]
	if dfid == "" {
		dfid = "-"
	}
	token := cookies["t"]
	vipType := 0
	if v := cookies["vip_type"]; v != "" {
		vipType, _ = strconv.Atoi(v)
	}
	albumAudioID, err := p.kugouAlbumAudioID(id)
	if err != nil || albumAudioID <= 0 {
		return Stream{}, ErrNotFound
	}

	now := time.Now()
	clienttime := kgClienttime(now.Unix())
	clienttimeMS := now.UnixMilli()
	hashLower := strings.ToLower(id)
	body := map[string]any{
		"area_code": "1",
		"behavior":  "play",
		"qualities": []string{"128", "320", "flac", "high", "viper_atmos", "viper_tape", "viper_clear", "super"},
		"resource": map[string]any{
			"album_audio_id":  albumAudioID,
			"collect_list_id": "3",
			"collect_time":    clienttimeMS,
			"hash":            hashLower,
			"id":              0,
			"page_id":         1,
			"type":            "audio",
		},
		"token": token,
		"tracker_param": map[string]any{
			"all_m":         1,
			"auth":          "",
			"is_free_part":  0,
			"key":           kgSignKeyLite(hashLower, mid, userID),
			"module_id":     0,
			"need_climax":   1,
			"need_xcdn":     1,
			"open_time":     "",
			"pid":           "411",
			"pidversion":    "3001",
			"priv_vip_type": "6",
			"viptoken":      vipToken,
		},
		"userid": userID,
		"vip":    vipType,
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		return Stream{}, err
	}

	query := map[string]string{
		"dfid":       dfid,
		"mid":        mid,
		"uuid":       "-",
		"appid":      kgLiteAppID,
		"clientver":  kgLiteClientver,
		"clienttime": clienttime,
		"token":      token,
		"userid":     userID,
	}
	query["signature"] = kgSignAndroidLite(query, string(rawBody))

	values := url.Values{}
	for key, value := range query {
		values.Set(key, value)
	}
	endpoint := "http://tracker.kugou.com/v6/priv_url?" + values.Encode()
	headers := map[string]string{
		"User-Agent": kgUserAgent(),
		"dfid":       dfid,
		"clienttime": clienttime,
		"mid":        mid,
		"Cookie":     p.cookie(),
	}
	data, err := httpPostRaw(context.Background(), endpoint, rawBody, headers)
	if err != nil {
		return Stream{}, err
	}
	var resp struct {
		Status    int `json:"status"`
		ErrorCode int `json:"error_code"`
		Data      []struct {
			URL      string `json:"url"`
			FileSize int64  `json:"fileSize"`
			BitRate  int    `json:"bitRate"`
			ExtName  string `json:"extName"`
			Quality  string `json:"quality"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return Stream{}, err
	}
	if resp.Status != 1 || len(resp.Data) == 0 {
		return Stream{}, ErrNotFound
	}
	want := kgMapQuality(quality)
	best := Stream{}
	for _, item := range resp.Data {
		if item.URL == "" {
			continue
		}
		br := item.BitRate / 1000
		if item.Quality == want || best.URL == "" || br > best.BR {
			fmtName := item.ExtName
			if fmtName == "" {
				fmtName = "mp3"
			}
			best = Stream{
				URL:     item.URL,
				Size:    item.FileSize,
				BR:      br,
				Format:  fmtName,
				Quality: NormalizeQuality(quality),
			}
			if item.Quality == want {
				break
			}
		}
	}
	if best.URL == "" {
		return Stream{}, ErrNotFound
	}
	return best, nil
}

func (p *KugouProvider) kugouAlbumAudioID(hash string) (int64, error) {
	userid := "0"
	vip := 0
	if p.cookie() != "" {
		vip = 1
		cookies := parseKugouCookie(p.cookie())
		if v := cookies["KugooID"]; v != "" {
			userid = v
		}
	}
	request := map[string]any{
		"relate":    1,
		"userid":    userid,
		"vip":       vip,
		"appid":     1000,
		"token":     "",
		"behavior":  "play",
		"area_code": "1",
		"clientver": "8990",
		"resource": []map[string]any{
			{"id": 0, "type": "audio", "hash": hash},
		},
	}
	body, err := json.Marshal(request)
	if err != nil {
		return 0, err
	}
	data, err := httpPostRaw(context.Background(), "http://media.store.kugou.com/v1/get_res_privilege", body, p.headers())
	if err != nil {
		return 0, err
	}
	var payload struct {
		Data []struct {
			AlbumAudioID int64 `json:"album_audio_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return 0, err
	}
	if len(payload.Data) == 0 || payload.Data[0].AlbumAudioID <= 0 {
		return 0, fmt.Errorf("album_audio_id not found")
	}
	return payload.Data[0].AlbumAudioID, nil
}

// streamLegacy falls back to the older get_res_privilege + trackercdn flow.
// This still works for tracks that don't require VIP/region authorisation.
func (p *KugouProvider) streamLegacy(id string) (Stream, error) {
	userid := "0"
	vip := 0
	if p.cookie() != "" {
		vip = 1
		cookies := parseKugouCookie(p.cookie())
		if v := cookies["KugooID"]; v != "" {
			userid = v
		}
	}
	request := map[string]any{
		"relate":    1,
		"userid":    userid,
		"vip":       vip,
		"appid":     1000,
		"token":     "",
		"behavior":  "play",
		"area_code": "1",
		"clientver": "8990",
		"resource": []map[string]any{
			{"id": 0, "type": "audio", "hash": id},
		},
	}
	body, err := json.Marshal(request)
	if err != nil {
		return Stream{}, err
	}
	data, err := httpPostRaw(context.Background(), "http://media.store.kugou.com/v1/get_res_privilege", body, p.headers())
	if err != nil {
		return Stream{}, err
	}
	var payload struct {
		Data []struct {
			RelateGoods []struct {
				Hash string `json:"hash"`
				Info struct {
					Bitrate int `json:"bitrate"`
				} `json:"info"`
			} `json:"relate_goods"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return Stream{}, err
	}
	if len(payload.Data) == 0 {
		return Stream{}, ErrNotFound
	}
	best := Stream{}
	for _, item := range payload.Data[0].RelateGoods {
		tracker, err := httpGet(context.Background(), "http://trackercdn.kugou.com/i/v2/", map[string]string{
			"hash":     item.Hash,
			"key":      md5Hex(item.Hash + "kgcloudv2"),
			"pid":      "3",
			"behavior": "play",
			"cmd":      "25",
			"version":  "8990",
		}, p.headers())
		if err != nil {
			continue
		}
		var response struct {
			URL      []string `json:"url"`
			FileSize int64    `json:"fileSize"`
			BitRate  int      `json:"bitRate"`
		}
		if json.Unmarshal(tracker, &response) != nil || len(response.URL) == 0 {
			continue
		}
		if response.BitRate/1000 >= best.BR {
			fmt := "mp3"
			q := QualityStandard
			switch {
			case response.BitRate/1000 >= 800:
				fmt, q = "flac", QualityLossless
			case response.BitRate/1000 >= 320:
				fmt, q = "mp3", QualityExhigh
			}
			best = Stream{
				URL:     response.URL[0],
				Size:    response.FileSize,
				BR:      response.BitRate / 1000,
				Format:  fmt,
				Quality: q,
			}
		}
	}
	if best.URL == "" {
		return Stream{}, ErrNotFound
	}
	return best, nil
}

func (p *KugouProvider) Cover(id string, size int) (string, error) {
	track, err := p.fetchTrack(id)
	if err != nil {
		return "", err
	}
	image := normalizeURL(strings.ReplaceAll(track.ImgURL, "{size}", strconv.Itoa(size)))
	if image == "" {
		return "", ErrNotFound
	}
	return image, nil
}

func (p *KugouProvider) Lyric(id string) (Lyrics, error) {
	search, err := httpGet(context.Background(), "http://krcs.kugou.com/search", map[string]string{
		"keyword": "%20-%20",
		"ver":     "1",
		"hash":    id,
		"client":  "mobi",
		"man":     "yes",
	}, p.headers())
	if err != nil {
		return Lyrics{}, err
	}
	var payload struct {
		Candidates []struct {
			AccessKey string `json:"accesskey"`
			ID        string `json:"id"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(search, &payload); err != nil {
		return Lyrics{}, err
	}
	if len(payload.Candidates) == 0 {
		return Lyrics{Merged: ""}, nil
	}
	download, err := httpGet(context.Background(), "http://lyrics.kugou.com/download", map[string]string{
		"charset":   "utf8",
		"accesskey": payload.Candidates[0].AccessKey,
		"id":        payload.Candidates[0].ID,
		"client":    "mobi",
		"fmt":       "lrc",
		"ver":       "1",
	}, p.headers())
	if err != nil {
		return Lyrics{}, err
	}
	var response struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(download, &response); err != nil {
		return Lyrics{}, err
	}
	primary, _ := base64StdDecode(response.Content)
	return Lyrics{Primary: primary, Merged: primary}, nil
}

func (p *KugouProvider) fetchTrack(id string) (kugouTrack, error) {
	data, err := httpGet(context.Background(), "http://m.kugou.com/app/i/getSongInfo.php", map[string]string{
		"cmd":  "playInfo",
		"hash": id,
		"from": "mkugou",
	}, p.headers())
	if err != nil {
		return kugouTrack{}, err
	}
	var track kugouTrack
	if err := json.Unmarshal(data, &track); err != nil {
		return kugouTrack{}, err
	}
	if track.Hash == "" && firstNonEmpty(track.Filename, track.FileName) == "" {
		return kugouTrack{}, ErrNotFound
	}
	if track.Hash == "" {
		track.Hash = id
	}
	return track, nil
}

func (p *KugouProvider) mapTracks(items []kugouTrack) []Song {
	songs := make([]Song, 0, len(items))
	for _, item := range items {
		songs = append(songs, p.mapTrack(item))
	}
	return songs
}

func (p *KugouProvider) mapTrack(item kugouTrack) Song {
	name := firstNonEmpty(item.Filename, item.FileName)
	artists := []string{}
	if name == "" {
		name = item.SongName
		if item.SingerName != "" {
			artists = strings.Split(item.SingerName, "、")
		}
	}
	if left, right, ok := strings.Cut(name, " - "); ok {
		artists = strings.Split(left, "、")
		name = right
	}
	duration := item.Duration
	if duration == 0 {
		duration = item.TimeLength
	}
	return Song{
		ID:       item.Hash,
		Name:     name,
		Artists:  sanitizeArtists(artists),
		Album:    item.AlbumName,
		Duration: duration,
		CoverID:  item.Hash,
		StreamID: item.Hash,
		LyricID:  item.Hash,
		Provider: "kugou",
	}
}
