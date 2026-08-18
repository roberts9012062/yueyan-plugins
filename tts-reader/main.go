// marketplace-repo/tts-reader/main.go
// TTS 朗读插件（进程外）：帖子正文一键语音朗读。
//
// 能力划分：
//   - api：POST /tts（合成 → 返回音频 id）+ POST /tts/audio（按 id 取音频字节）
//   - frontend：post.footer 槽位渲染「🔊 朗读」工具条（抓正文 → 合成 → Blob URL 播放）
//   - settings：默认音色 / 默认语速 / 单次最大字数 / 可选自定义合成端点（留空用内置免费引擎）
//
// 访客通道（无需登录）：宿主公开端点 /api/v1/tts 与 /api/v1/tts/audio 以 System 身份
// 桥接本插件（与 bilibili-video 游客通道同模式，见 internal/handler/video.go）。
// 音频按正文哈希缓存到插件数据目录（缓存即文件系统：重启可复用、天然防路径穿越）。
//
// 合成引擎：内置微软 Edge readaloud 免费 WebSocket 端点（无需 Key，中文多音色）；
//
//	设置自定义端点后改走自定义（POST JSON {text,voice,rate} → 返回音频字节）。
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
	"github.com/roberts9012062/boke/pkg/plugin-sdk/server"
)

// pluginID 插件唯一 ID（与 yueyan-plugin.json / plugin.json 一致）。
const pluginID = "tts-reader"

// voiceOptions Edge 常见中文音色（设置项 select 选项）。
var voiceOptions = []string{
	"zh-CN-XiaoxiaoNeural", "zh-CN-XiaoyiNeural", "zh-CN-YunjianNeural",
	"zh-CN-YunxiNeural", "zh-CN-YunyangNeural", "zh-CN-liaoning-XiaobeiNeural",
}

// TtsPlugin TTS 朗读插件实现（进程外）。
type TtsPlugin struct {
	mu    sync.Mutex
	store *ttsStore // 音频缓存目录句柄（OnActivate 初始化；未激活为 nil 需判空）
}

// Info 插件信息（与商城清单一致；能力声明 + 设置项）。
func (p *TtsPlugin) Info() sdk.Info {
	return sdk.Info{
		ID:           pluginID,
		Name:         "朗读",
		Version:      "1.0.0",
		Author:       "月言",
		Description:  "帖子正文语音朗读：访客一键听全文，内置免费 Edge 引擎，可选自定义合成端点。",
		Capabilities: []string{"api", "frontend", "settings"},
		Settings: []sdk.SettingField{
			{Key: "default_voice", Label: "默认音色", Type: "select", Default: "zh-CN-XiaoxiaoNeural", Options: voiceOptions},
			{Key: "default_rate", Label: "默认语速", Type: "select", Default: "+0%", Options: []string{"-20%", "-10%", "+0%", "+10%", "+20%"}},
			{Key: "max_chars", Label: "单次朗读最大字数", Type: "text", Default: "3000"},
			{Key: "custom_endpoint", Label: "自定义合成端点（留空用内置免费引擎）", Type: "text", Default: ""},
			{Key: "custom_key", Label: "自定义端点 API Key（可选）", Type: "text", Default: ""},
		},
	}
}

// OnActivate 启用回调：初始化音频缓存目录（并行示例路径 data/plugins/tts-reader/cache）。
func (p *TtsPlugin) OnActivate(ctx context.Context) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	dir := filepath.Join(wd, "data", "plugins", pluginID, "cache")
	store, err := newTTSStore(dir)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.store = store
	p.mu.Unlock()
	return nil
}

// OnDeactivate 停用回调：释放缓存句柄（磁盘缓存保留，重启可复用）。
func (p *TtsPlugin) OnDeactivate(ctx context.Context) error {
	p.mu.Lock()
	p.store = nil
	p.mu.Unlock()
	return nil
}

// Hooks 订阅钩子（本插件无需钩子，朗读属纯读流程不插入业务管道）。
func (p *TtsPlugin) Hooks() []sdk.Hook { return nil }

// storeSafe 取缓存句柄（未激活返回 nil，调用方判空）。
func (p *TtsPlugin) storeSafe() *ttsStore {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.store
}

// RegisterAPI 自定义 API（主进程挂 /api/v1/plugins/tts-reader/** 代理；宿主公开端点以
// System 身份桥接同名路径给访客）。端点均为固定路径（SDK APIMux 精确匹配，无通配）：
//
//	POST /tts        合成（body {text, voice?, rate?}）→ {id} 或 {error}
//	POST /tts/audio  取音频（body {id}）→ 原始音频字节（audio/mpeg）
func (p *TtsPlugin) RegisterAPI(api *sdk.APIMux) {
	api.Handle("POST", "/tts", p.handleSynth)
	api.Handle("POST", "/tts/audio", p.handleAudio)
}

// main 插件进程入口（server.Serve 完成握手与契约服务注册）。
func main() {
	fmt.Fprintln(os.Stderr, "[tts-reader] 进程启动")
	server.Serve(&TtsPlugin{})
}
