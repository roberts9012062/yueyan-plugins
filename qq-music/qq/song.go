// cmd/qq-music-plugin/qq/song.go
// QQ 音乐歌曲查询：搜索 / 播放地址（vkey 拼装）。
package qq

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Song 歌曲信息（发帖音乐引用 + 播放器展示）。
type Song struct {
	SongMID string `json:"song_mid"` // 歌曲 MID（vkey 用）
	SongID  int64  `json:"song_id"`  // 数字 ID
	Name    string `json:"name"`     // 歌名
	Artist  string `json:"artist"`   // 歌手（多歌手 / 分隔）
	Album   string `json:"album"`    // 专辑
	Cover   string `json:"cover"`    // 封面
}

// SongURL 播放地址。
type SongURL struct {
	SongMID string `json:"song_mid"`
	URL     string `json:"url"` // 播放直链（带 vkey，有时效）
}

// searchResp 搜索接口响应（仅取用到的字段）。
type searchResp struct {
	Data struct {
		Song struct {
			List []struct {
				SongMID   string `json:"songmid"`
				SongID    int64  `json:"songid"`
				SongName  string `json:"songname"`
				AlbumName string `json:"albumname"`
				AlbumMID  string `json:"albummid"`
				Singer    []struct {
					Name string `json:"name"`
				} `json:"singer"`
			} `json:"list"`
		} `json:"song"`
	} `json:"data"`
}

// coverURL 生成专辑封面 URL（纯函数；albummid 空返回空）。
func coverURL(albummid string) string {
	if albummid == "" {
		return ""
	}
	return "https://y.gtimg.cn/music/photo_new/T002R300x300M000" + albummid + ".jpg"
}

// joinArtists 拼接歌手名（多歌手 / 分隔；纯函数）。
func joinArtists(artists []string) string {
	return strings.Join(artists, " / ")
}

// Search 搜索歌曲（keyword 关键词；limit 返回条数，默认 10）。
func (c *Client) Search(keyword string, limit int) ([]Song, error) {
	if limit <= 0 {
		limit = 10
	}
	rawURL := searchURL + "?format=json&w=" + url.QueryEscape(keyword) + "&n=" + fmt.Sprint(limit) + "&cr=1"
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36")
	req.Header.Set("Referer", "https://y.qq.com/")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("QQ音乐搜索请求失败: %w", err)
	}
	defer resp.Body.Close()
	bodyRaw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var sr searchResp
	if err := json.Unmarshal(bodyRaw, &sr); err != nil {
		return nil, fmt.Errorf("搜索响应解析失败: %w", err)
	}
	songs := make([]Song, 0, len(sr.Data.Song.List))
	for _, s := range sr.Data.Song.List {
		artists := make([]string, 0, len(s.Singer))
		for _, a := range s.Singer {
			artists = append(artists, a.Name)
		}
		songs = append(songs, Song{
			SongMID: s.SongMID, SongID: s.SongID, Name: s.SongName,
			Artist: joinArtists(artists), Album: s.AlbumName, Cover: coverURL(s.AlbumMID),
		})
	}
	return songs, nil
}

// SongURL 获取播放地址（vkey 拼装；地址有时效，按需获取）。
// 参数：songmid 歌曲 MID。
func (c *Client) SongURL(songmid string) (*SongURL, error) {
	vkey, filename, sip, guid, err := c.GetVkey(songmid)
	if err != nil {
		return nil, err
	}
	if sip == "" {
		sip = "http://aqqmusic.tc.qq.com/"
	}
	c.mu.Lock()
	uin := c.uin
	c.mu.Unlock()
	// 播放地址：sip + filename + vkey 等参数（guid 必须复用 vkey 请求时的值，否则 CDN 返回 403；
	// http→https 转换，生产 https 页面加载 http 音频会被混合内容拦截）
	u := sip + filename + "?vkey=" + vkey + "&guid=" + guid + "&uin=" + uin + "&fromtag=8"
	u = toHTTPS(u)
	return &SongURL{SongMID: songmid, URL: u}, nil
}

// toHTTPS 将 http 协议地址转为 https（腾讯 CDN 支持 https；纯函数）。
func toHTTPS(raw string) string {
	if len(raw) >= 7 && raw[:7] == "http://" {
		return "https://" + raw[7:]
	}
	return raw
}
// lyricResp 歌词接口响应（仅取 lyric 字段）。
type lyricResp struct {
	Lyric string `json:"lyric"`
}

// lyricMeta 从歌词 LRC 提取歌名/歌手（纯函数；[ti:xxx] [ar:xxx]）。
func lyricMeta(lrc string) (string, string) {
	title, artist := "", ""
	if i := strings.Index(lrc, "[ti:"); i >= 0 {
		rest := lrc[i+4:]
		if j := strings.Index(rest, "]"); j > 0 {
			title = rest[:j]
		}
	}
	if i := strings.Index(lrc, "[ar:"); i >= 0 {
		rest := lrc[i+4:]
		if j := strings.Index(rest, "]"); j > 0 {
			artist = rest[:j]
		}
	}
	return title, artist
}

// fetchLyric 拉取歌词并提取歌名/歌手（纯函数式；歌词接口按 songmid 查）。
func (c *Client) fetchLyric(songmid string) (string, string, error) {
	rawURL := "https://c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg?songmid=" + url.QueryEscape(songmid) + "&format=json&nobase64=1"
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36")
	req.Header.Set("Referer", "https://y.qq.com/")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("QQ音乐歌词请求失败: %w", err)
	}
	defer resp.Body.Close()
	bodyRaw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", "", err
	}
	var lr lyricResp
	if err := json.Unmarshal(bodyRaw, &lr); err != nil {
		return "", "", fmt.Errorf("歌词响应解析失败: %w", err)
	}
	title, artist := lyricMeta(lr.Lyric)
	if title == "" && artist == "" {
		return "", "", fmt.Errorf("歌词未找到歌名/歌手")
	}
	return title, artist, nil
}

// SongDetail 查询歌曲详情（歌词接口取歌名/歌手 + 搜索接口匹配取封面）。
func (c *Client) SongDetail(songmid string) (*Song, error) {
	title, artist, err := c.fetchLyric(songmid)
	if err != nil {
		return nil, err
	}
	// 搜索接口按歌名+歌手匹配 songmid，取 album/封面
	album, cover := "", ""
	if songs, err := c.Search(title+" "+artist, 10); err == nil {
		for _, s := range songs {
			if s.SongMID == songmid {
				album = s.Album
				cover = s.Cover
				break
			}
		}
	}
	return &Song{SongMID: songmid, Name: title, Artist: artist, Album: album, Cover: cover}, nil
}

