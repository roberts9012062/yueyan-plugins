// cmd/bilibili-video-plugin/main.go
// B 站视频插件（进程外）：站长扫码/短信登录 B 站 → 帖内嵌入 B 站视频并选择清晰度
// （360P~1080P）→ 游客经站长账号或自己的 B 站账号（guest_token）观看高清。
//
// 能力：api + frontend（播放器内容块 + 后台登录页）+ settings + admin.page。
// 登录态（SESSDATA 等 cookie）AES 加密持久化到 data/plugins/bilibili-video/state.json；
// 游客登录态不落盘，经 AES 封装为 guest_token 由浏览器持有。
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/skip2/go-qrcode"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
	"github.com/roberts9012062/boke/pkg/plugin-sdk/server"

	"github.com/roberts9012062/yueyan-plugins/bilibili-video/bilibili"
)

// pluginID 插件唯一 ID（与清单一致）。
const pluginID = "bilibili-video"

// BilibiliPlugin B 站视频插件实现（进程外）。
type BilibiliPlugin struct {
	mu     sync.Mutex
	client *bilibili.Client
}

// Info 插件信息（与商城清单一致；能力 + 设置项）。
func (p *BilibiliPlugin) Info() sdk.Info {
	return sdk.Info{
		ID:          pluginID,
		Name:        "B站视频",
		Version:     "0.1.1",
		Author:      "月言官方",
		Description: "B站视频嵌入：站长扫码登录后，发帖可插入 B 站视频并选择 360P~1080P 清晰度，游客可观看高清。",
		Capabilities: []string{"hooks", "api", "frontend", "settings", "admin.page"},
		Settings: []sdk.SettingField{
			{Key: "default_quality", Label: "默认清晰度", Type: "select", Default: "80",
				Options: []string{"16", "32", "64", "80"}},
			{Key: "allow_guest_hd", Label: "允许游客用站长账号看高清", Type: "switch", Default: "on"},
		},
	}
}

// OnActivate 启用回调：创建客户端并恢复登录态。
func (p *BilibiliPlugin) OnActivate(ctx context.Context) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	dir := filepath.Join(wd, "data", "plugins", pluginID)
	client, err := bilibili.NewClient(dir)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.client = client
	p.mu.Unlock()
	return nil
}

// OnDeactivate 停用回调：释放客户端。
func (p *BilibiliPlugin) OnDeactivate(ctx context.Context) error {
	p.mu.Lock()
	p.client = nil
	p.mu.Unlock()
	return nil
}

// Hooks 订阅钩子（本插件无钩子需求，清晰度选择在编辑器完成）。
func (p *BilibiliPlugin) Hooks() []sdk.Hook { return nil }

// jsonResp 便捷构造 JSON 响应（纯函数）。
func jsonResp(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}

// clientSafe 取当前客户端（未激活时返回 nil）。
func (p *BilibiliPlugin) clientSafe() *bilibili.Client {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.client
}

// qrPNGDataURL 把扫码内容渲染为二维码 PNG dataURL（纯函数）。
func qrPNGDataURL(content string) (string, error) {
	png, err := qrcode.Encode(content, qrcode.Medium, 240)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

// main 插件进程入口。
func main() {
	fmt.Fprintln(os.Stderr, "[bilibili-video] 进程启动")
	server.Serve(&BilibiliPlugin{})
}
