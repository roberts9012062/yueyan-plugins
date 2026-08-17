// cmd/qq-music-plugin/qq/playlist.go
// QQ 音乐用户歌单：创建的歌单列表（含「我喜欢」）+ 歌单内歌曲（登录态接口，分页拉全）。
package qq

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Playlist 用户歌单。
type Playlist struct {
	Tid     int64  `json:"tid"`      // 歌单 ID（详情接口用）
	Name    string `json:"name"`     // 歌单名
	Cover   string `json:"cover"`    // 封面
	SongCnt int    `json:"song_cnt"` // 歌曲数
}

// PlaylistSong 歌单内歌曲。
type PlaylistSong struct {
	SongMID string `json:"song_mid"` // 歌曲 MID（播放地址用）
	Name    string `json:"name"`     // 歌名
	Artist  string `json:"artist"`   // 歌手（多歌手 / 分隔）
	Album   string `json:"album"`    // 专辑
	Cover   string `json:"cover"`    // 封面
}

// createdDissResp 创建歌单列表响应（c.y.qq.com；响应即 UTF-8）。
type createdDissResp struct {
	Code int `json:"code"`
	Data struct {
		EncryptUin string `json:"encrypt_uin"`
		Disslist   []struct {
			DissName  string `json:"diss_name"`
			DissCover string `json:"diss_cover"`
			SongCnt   int    `json:"song_cnt"`
			Tid       int64  `json:"tid"`
			DirShow   int    `json:"dir_show"`
		} `json:"disslist"`
	} `json:"data"`
}

// dissResp 歌单详情响应（qzone fcg；utf8=1 已转 UTF-8）。
type dissResp struct {
	Code   int `json:"code"`
	Cdlist []struct {
		Dissname     string `json:"dissname"`
		Logo         string `json:"logo"`
		TotalSongNum int    `json:"total_song_num"`
		Songlist     []struct {
			SongMID   string `json:"songmid"`
			SongName  string `json:"songname"`
			AlbumMID  string `json:"albummid"`
			AlbumName string `json:"albumname"`
			Singer    []struct {
				Name string `json:"name"`
			} `json:"singer"`
		} `json:"songlist"`
	} `json:"cdlist"`
}

// favDirResp「我喜欢」目录信息响应（musicu.fcg CgiGetDiss，dirid=201）。
type favDirResp struct {
	DissInfo struct {
		Code int `json:"code"`
		Data struct {
			Dirinfo struct {
				ID      int64  `json:"id"`
				Title   string `json:"title"`
				Songnum int    `json:"songnum"`
				Picurl  string `json:"picurl"`
			} `json:"dirinfo"`
		} `json:"data"`
	} `json:"music.srfDissInfo.DissInfo"`
}

// favDirID「我喜欢」的固定 dirid（QQ 音乐约定）。
const favDirID = 201

// musicuComm 构造 musicu.fcg 公共参数（登录态由 authst 携带；纯函数）。
func musicuComm(uin string, musickey string) map[string]any {
	loginType := "2"
	if len(musickey) >= 3 && musickey[:3] == "W_X" {
		loginType = "1"
	}
	return map[string]any{
		"cv": "4747474", "v": "4747474", "ct": "11", "tmeAppID": "qqmusic",
		"format": "json", "inCharset": "utf-8", "outCharset": "utf-8",
		"uid": uin, "qq": uin, "authst": musickey, "tmeLoginType": loginType,
	}
}

// Playlists 获取登录用户的歌单列表（「我喜欢」置顶 + 创建的歌单）。
func (c *Client) Playlists() ([]Playlist, error) {
	c.mu.Lock()
	uin := c.uin
	c.mu.Unlock()
	if uin == "" {
		return nil, fmt.Errorf("未登录，请先扫码登录")
	}
	// 1. 创建的歌单（响应含 encrypt_uin，供「我喜欢」接口用）
	rawURL := "https://c.y.qq.com/rsc/fcgi-bin/fcg_user_created_diss?hostuin=" + uin + "&size=50&page=1&format=json"
	bodyRaw, err := c.playlistGet(rawURL)
	if err != nil {
		return nil, err
	}
	var cr createdDissResp
	if err := json.Unmarshal(bodyRaw, &cr); err != nil {
		return nil, fmt.Errorf("歌单列表解析失败: %w", err)
	}
	if cr.Code != 0 {
		return nil, fmt.Errorf("歌单列表接口返回错误码 %d", cr.Code)
	}
	playlists := make([]Playlist, 0, len(cr.Data.Disslist)+1)
	// 2.「我喜欢」置顶（系统收藏歌单，dirid=201）
	if fav, err := c.favPlaylist(cr.Data.EncryptUin); err == nil && fav.Tid > 0 {
		playlists = append(playlists, fav)
	}
	// 3. 创建的歌单（过滤系统歌单 tid=0，如「QZone背景音乐」）
	for _, d := range cr.Data.Disslist {
		if d.Tid <= 0 {
			continue
		}
		playlists = append(playlists, Playlist{Tid: d.Tid, Name: d.DissName, Cover: d.DissCover, SongCnt: d.SongCnt})
	}
	return playlists, nil
}

// favPlaylist 获取「我喜欢」目录信息（id/标题/歌曲数/封面）。
func (c *Client) favPlaylist(encryptUin string) (Playlist, error) {
	c.mu.Lock()
	uin := c.uin
	musickey := c.musickey
	c.mu.Unlock()
	payload := map[string]any{
		"comm": musicuComm(uin, musickey),
		"music.srfDissInfo.DissInfo": map[string]any{
			"method": "CgiGetDiss",
			"module": "music.srfDissInfo.DissInfo",
			"param": map[string]any{
				"disstid": 0, "dirid": favDirID, "tag": true,
				"song_begin": 0, "song_num": 0,
				"userinfo": true, "orderlist": true,
				"enc_host_uin": encryptUin,
			},
		},
	}
	bodyRaw, err := c.musicuPost(payload)
	if err != nil {
		return Playlist{}, err
	}
	var fr favDirResp
	if err := json.Unmarshal(bodyRaw, &fr); err != nil {
		return Playlist{}, fmt.Errorf("我喜欢解析失败: %w", err)
	}
	d := fr.DissInfo.Data.Dirinfo
	if d.ID <= 0 {
		return Playlist{}, fmt.Errorf("我喜欢目录不存在（code=%d）", fr.DissInfo.Code)
	}
	cover := d.Picurl
	if cover == "" {
		cover = "https://y.gtimg.cn/mediastyle/y/img/cover_love_300.jpg"
	}
	return Playlist{Tid: d.ID, Name: d.Title, Cover: cover, SongCnt: d.Songnum}, nil
}

// PlaylistSongs 获取歌单内全部歌曲（tid 歌单 ID；分页 500 首/次拉全）。
func (c *Client) PlaylistSongs(tid string) ([]PlaylistSong, error) {
	if _, err := strconv.ParseInt(tid, 10, 64); err != nil {
		return nil, fmt.Errorf("歌单 ID 无效")
	}
	const pageSize = 500
	all := make([]PlaylistSong, 0, pageSize)
	for begin := 0; ; begin += pageSize {
		songs, total, err := c.playlistSongsPage(tid, begin, pageSize)
		if err != nil {
			return nil, err
		}
		all = append(all, songs...)
		// 不足一页或已到总数即结束（total 未知时以空页结束）
		if len(songs) < pageSize || (total > 0 && len(all) >= total) {
			break
		}
	}
	return all, nil
}

// playlistSongsPage 拉取歌单一页歌曲（begin 起始、num 数量；返回本页、总数）。
func (c *Client) playlistSongsPage(tid string, begin int, num int) ([]PlaylistSong, int, error) {
	rawURL := "https://c.y.qq.com/qzone/fcg-bin/fcg_ucc_getcdinfo_byids_cp.fcg?type=1&json=1&utf8=1&onlysong=0&disstid=" + tid +
		"&song_begin=" + strconv.Itoa(begin) + "&song_num=" + strconv.Itoa(num) + "&format=json"
	bodyRaw, err := c.playlistGet(rawURL)
	if err != nil {
		return nil, 0, err
	}
	var dr dissResp
	if err := json.Unmarshal(bodyRaw, &dr); err != nil {
		return nil, 0, fmt.Errorf("歌单详情解析失败: %w", err)
	}
	if len(dr.Cdlist) == 0 {
		return nil, 0, fmt.Errorf("歌单不存在或已删除")
	}
	cd := dr.Cdlist[0]
	songs := make([]PlaylistSong, 0, len(cd.Songlist))
	for _, s := range cd.Songlist {
		artists := make([]string, 0, len(s.Singer))
		for _, a := range s.Singer {
			artists = append(artists, a.Name)
		}
		songs = append(songs, PlaylistSong{
			SongMID: s.SongMID, Name: s.SongName, Artist: joinArtists(artists),
			Album: s.AlbumName, Cover: coverURL(s.AlbumMID),
		})
	}
	return songs, cd.TotalSongNum, nil
}

// playlistGet 发起歌单相关 GET 请求（带 QQ 音乐 UA/Referer；返回响应体）。
func (c *Client) playlistGet(rawURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36")
	req.Header.Set("Referer", "https://y.qq.com/")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("QQ音乐接口请求失败: %w", err)
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

// musicuPost 发起 musicu.fcg POST 请求（JSON；返回响应体）。
func (c *Client) musicuPost(payload map[string]any) ([]byte, error) {
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, vkeyBaseURL, strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36")
	req.Header.Set("Referer", "https://y.qq.com/")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("musicu 请求失败: %w", err)
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

