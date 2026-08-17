// cmd/netease-music-plugin/netease/song.go
// 网易云音乐歌曲查询：搜索 / 详情 / 播放地址 / 歌词（weapi 加密）。
package netease

import (
	"encoding/json"
	"fmt"
	"time"
)

// Song 歌曲信息（发帖音乐引用 + 播放器展示）。
type Song struct {
	ID        int64  `json:"id"`         // 歌曲 ID
	Name      string `json:"name"`       // 歌名
	Artist    string `json:"artist"`     // 歌手（多歌手用 / 分隔）
	Album     string `json:"album"`      // 专辑
	CoverURL  string `json:"cover_url"`  // 封面
	Duration  int64  `json:"duration"`   // 时长（毫秒）
}

// SongURL 播放地址（带有效期）。
type SongURL struct {
	ID   int64  `json:"id"`   // 歌曲 ID
	URL  string `json:"url"`  // 播放地址（mp3 直链；空=无版权/需会员）
	Expi int64  `json:"expi"` // 有效期（秒）
}

// searchResp 搜索响应。
type searchResp struct {
	Result struct {
		Songs []struct {
			ID      int64  `json:"id"`
			Name    string `json:"name"`
			Artists []struct {
				Name string `json:"name"`
			} `json:"artists"`
			Album struct {
				Name string `json:"name"`
			} `json:"album"`
			Duration int64 `json:"duration"`
		} `json:"songs"`
	} `json:"result"`
}

// songDetailResp 歌曲详情响应。
type songDetailResp struct {
	Songs []struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		Artists []struct {
			Name string `json:"name"`
		} `json:"ar"`
		Album struct {
			Name string `json:"name"`
			PicURL string `json:"picUrl"`
		} `json:"al"`
		Duration int64 `json:"dt"`
	} `json:"songs"`
}

// songURLResp 播放地址响应。
type songURLResp struct {
	Code int `json:"code"`
	Data []struct {
		ID   int64  `json:"id"`
		URL  string `json:"url"`
		Expi int64  `json:"expi"`
	} `json:"data"`
}

// joinArtists 拼接歌手名（多歌手 / 分隔；纯函数）。
func joinArtists(artists []string) string {
	out := ""
	for i, a := range artists {
		if i > 0 {
			out += " / "
		}
		out += a
	}
	return out
}

// Search 搜索歌曲（keyword 关键词；limit 返回条数，默认 10）。
func (c *Client) Search(keyword string, limit int) ([]Song, error) {
	if limit <= 0 {
		limit = 10
	}
	body, err := c.WeapiRequest("/weapi/search/get", map[string]any{
		"s":      keyword,
		"type":   1,
		"limit":  limit,
		"offset": 0,
	})
	if err != nil {
		return nil, err
	}
	var resp searchResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("搜索响应解析失败: %w", err)
	}
	songs := make([]Song, 0, len(resp.Result.Songs))
	for _, s := range resp.Result.Songs {
		artists := make([]string, 0, len(s.Artists))
		for _, a := range s.Artists {
			artists = append(artists, a.Name)
		}
		songs = append(songs, Song{
			ID: s.ID, Name: s.Name, Artist: joinArtists(artists),
			Album: s.Album.Name, Duration: s.Duration,
		})
	}
	return songs, nil
}

// SongDetail 查询歌曲详情（歌名/歌手/专辑/封面）。
func (c *Client) SongDetail(songID int64) (*Song, error) {
	cParam, _ := json.Marshal([]map[string]int64{{"id": songID}})
	body, err := c.WeapiRequest("/weapi/v3/song/detail", map[string]any{
		"c": string(cParam),
	})
	if err != nil {
		return nil, err
	}
	var resp songDetailResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("详情响应解析失败: %w", err)
	}
	if len(resp.Songs) == 0 {
		return nil, fmt.Errorf("歌曲不存在（id=%d）", songID)
	}
	s := resp.Songs[0]
	artists := make([]string, 0, len(s.Artists))
	for _, a := range s.Artists {
		artists = append(artists, a.Name)
	}
	return &Song{
		ID: s.ID, Name: s.Name, Artist: joinArtists(artists),
		Album: s.Album.Name, CoverURL: s.Album.PicURL, Duration: s.Duration,
	}, nil
}

// SongURL 获取播放地址（实时；地址带有效期 20 分钟，需按需获取/缓存刷新）。
// 参数：level 音质（standard/higher/exhigh；匿名会被降级）；匿名请求仍能拿免费歌地址。
// 缓存：结果按 songID 缓存，提前 60 秒过期刷新（减少对网易云接口的请求，避免风控 -460）。
func (c *Client) SongURL(songID int64, level string) (*SongURL, error) {
	if level == "" {
		level = "standard"
	}
	// 缓存命中（未过期且剩余 > 60 秒）直接返回
	c.mu.Lock()
	if entry, ok := c.urlCache[songID]; ok {
		remaining := time.Duration(entry.expi)*time.Second - time.Since(entry.fetchedAt)
		if remaining > 60*time.Second {
			c.mu.Unlock()
			return &SongURL{ID: songID, URL: entry.url, Expi: entry.expi}, nil
		}
	}
	c.mu.Unlock()

	ids, _ := json.Marshal([]int64{songID})
	body, err := c.WeapiRequest("/weapi/song/enhance/player/url/v1", map[string]any{
		"ids":        string(ids),
		"level":      level,
		"encodeType": "mp3",
		"csrf_token": "",
	})
	if err != nil {
		return nil, err
	}
	var resp songURLResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("播放地址响应解析失败: %w", err)
	}
	if resp.Code != 200 || len(resp.Data) == 0 {
		return nil, fmt.Errorf("获取播放地址失败（code=%d）", resp.Code)
	}
	// 写缓存（仅成功且有地址时；http→https 转换——前端 CSP 仅放行 https 网易云 CDN，
	// 且生产 https 页面加载 http 音频会被浏览器按混合内容拦截）
	c.mu.Lock()
	c.urlCache[songID] = urlCacheEntry{url: toHTTPS(resp.Data[0].URL), expi: resp.Data[0].Expi, fetchedAt: time.Now()}
	c.mu.Unlock()
	return &SongURL{ID: resp.Data[0].ID, URL: toHTTPS(resp.Data[0].URL), Expi: resp.Data[0].Expi}, nil
}

// toHTTPS 将 http 协议地址转为 https（网易云 CDN 支持 https；纯函数）。
func toHTTPS(raw string) string {
	if len(raw) >= 7 && raw[:7] == "http://" {
		return "https://" + raw[7:]
	}
	return raw
}
