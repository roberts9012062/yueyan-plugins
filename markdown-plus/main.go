// marketplace-repo/markdown-plus/main.go
// Markdown 增强插件（进程外，免费）：帖子正文的展示级增强。
//
// 能力划分：
//   - hooks：订阅 content.render（waterfall 链式改写）——外链安全、图片懒加载、
//     标题锚点三项增强，各自可开关；互不依赖、幂等可叠加
//   - settings：三项增强开关（默认全开）+ 外链 nofollow（默认关）
//
// 说明：钩子作用于后端渲染管道的正文（HTML 帖直接生效；Markdown 源文本不匹配
// 任何模式时原样返回，无副作用）。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
	"github.com/roberts9012062/boke/pkg/plugin-sdk/server"
)

// pluginID 插件唯一 ID（与 plugin.json / yueyan-plugin.json 一致）。
const pluginID = "markdown-plus"

// MarkdownPlusPlugin Markdown 增强插件实现（进程外）。
type MarkdownPlusPlugin struct{}

// Info 插件信息（与商城清单一致；能力声明 + 设置项）。
func (p *MarkdownPlusPlugin) Info() sdk.Info {
	return sdk.Info{
		ID:           pluginID,
		Name:         "Markdown 增强",
		Version:      "3.1.0",
		Author:       "月言官方",
		Description:  "帖子正文展示增强：外链新窗口安全打开、图片懒加载、标题锚点定位。免费。",
		Capabilities: []string{"hooks", "settings"},
		Settings: []sdk.SettingField{
			{Key: "external_links", Label: "外链新窗口打开（加 target/rel 安全属性）", Type: "switch", Default: "on"},
			{Key: "lazy_images", Label: "图片懒加载（loading=lazy）", Type: "switch", Default: "on"},
			{Key: "heading_anchors", Label: "标题锚点（h1-h6 加 id 便于定位分享）", Type: "switch", Default: "on"},
			{Key: "link_nofollow", Label: "外链加 nofollow（防 SEO 外链权重流失）", Type: "switch", Default: "off"},
		},
	}
}

// OnActivate 启用回调（无资源需初始化）。
func (p *MarkdownPlusPlugin) OnActivate(ctx context.Context) error { return nil }

// OnDeactivate 停用回调。
func (p *MarkdownPlusPlugin) OnDeactivate(ctx context.Context) error { return nil }

// Hooks 订阅钩子：content.render（waterfall 链式改写正文）。
func (p *MarkdownPlusPlugin) Hooks() []sdk.Hook {
	return []sdk.Hook{
		{
			Name: "content.render", Sync: true, Priority: 100,
			Handler: handleRender,
		},
	}
}

// renderPayload content.render 载荷（与主进程 PostDetail 的 Dispatch 对齐）。
type renderPayload struct {
	PostID  int64  `json:"post_id"`
	Content string `json:"content"`
}

// handleRender 正文增强入口：解析载荷 → 按开关逐项增强 → Modify 回传改写结果。
// 载荷异常或未命中任何模式时原样放行（增强是锦上添花，绝不阻断渲染）。
func handleRender(ctx context.Context, ev sdk.Event) (sdk.Result, error) {
	var payload renderPayload
	if err := json.Unmarshal(ev.Payload, &payload); err != nil || payload.Content == "" {
		return sdk.Result{OK: true}, nil
	}
	content := payload.Content
	cfg := sdk.Config(ctx)
	if switchOn(cfg, "external_links") {
		content = enhanceExternalLinks(content)
	}
	if switchOn(cfg, "link_nofollow") {
		content = addLinkNofollow(content)
	}
	if switchOn(cfg, "lazy_images") {
		content = enhanceLazyImages(content)
	}
	if switchOn(cfg, "heading_anchors") {
		content = addHeadingAnchors(content)
	}
	modified, _ := json.Marshal(map[string]any{"content": content})
	return sdk.Result{OK: true, Modify: modified}, nil
}

// main 插件进程入口（server.Serve 完成握手与契约服务注册）。
func main() {
	fmt.Fprintln(os.Stderr, "[markdown-plus] 进程启动")
	server.Serve(&MarkdownPlusPlugin{})
}
