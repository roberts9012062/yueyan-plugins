// cmd/qq-music-plugin/qq/settings.go
// 首页背景音乐设置：开关 + 歌单选择（明文 JSON 持久化到插件数据目录）。
package qq

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// bgmSettings 首页背景音乐设置。
type bgmSettings struct {
	Enabled     bool   `json:"enabled"`       // 是否开启首页背景音乐
	PlaylistTid string `json:"playlist_tid"` // 选作背景音乐的歌单 ID
}

// BgmSettings 读取设置（无文件/损坏时返回默认值）。
func (c *Client) BgmSettings() bgmSettings {
	raw, err := os.ReadFile(filepath.Join(c.stateDir, "settings.json"))
	if err != nil {
		return bgmSettings{}
	}
	var s bgmSettings
	if err := json.Unmarshal(raw, &s); err != nil {
		return bgmSettings{}
	}
	return s
}

// SaveBgmSettings 保存设置（覆盖写；纯数据持久化）。
func (c *Client) SaveBgmSettings(s bgmSettings) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(c.stateDir, "settings.json"), raw, 0o644)
}

