// nav-links/main.go
// 精品导航插件（进程外）：后台收藏站点管理 + 前台公开/私有导航页 + 开放接口。
//
// 功能面：
//   - 后台管理页 /admin/plugin-pages/nav-links/admin：站点增删改查（含开放/私有可见性）、
//     分类/标签筛选、AI 智能分类+标签（data.read 经数据服务调主进程 AI）、自动抓取站点图标、
//     私有导航访问设置（仅自己可见 / 密码访问）；
//   - 前台公开页 /plugins/nav-links/index（site.page）：分类 Tab + 标签筛选 + 搜索，
//     卡片网格展示开放收藏站点；经宿主公开桥接 GET /api/v1/nav/links 读数据（访客可用）；
//   - 前台私有页 /plugins/nav-links/private（site.page）：私有收藏站点展示，
//     门禁经宿主桥接 /api/v1/nav/private/**（self=站长登录态；password=访问密码解锁 token）；
//   - 开放网关 GET/POST /api/v1/open/nav/links（navlinks.list / navlinks.save）：
//     浏览器插件凭 X-Api-Key 同步导航数据（默认开放口径）。
//
// 能力：api + frontend + settings + data.read + admin.page + site.page（不用钩子）。
// 存储：插件数据目录 data/plugins/nav-links/links.json + private.json（JSON 文件，原子写）。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
	"github.com/roberts9012062/boke/pkg/plugin-sdk/server"
)

// pluginID 插件唯一 ID（与清单一致）。
const pluginID = "nav-links"

// NavLinksPlugin 精品导航插件实现（进程外）。
type NavLinksPlugin struct {
	mu    sync.Mutex
	store *LinkStore    // 链接存储（激活时创建）
	priv  *PrivateStore // 私有访问配置存储（激活时创建）
}

// Info 插件信息（与商城清单一致；能力 + 设置项）。
func (p *NavLinksPlugin) Info() sdk.Info {
	return sdk.Info{
		ID:          pluginID,
		Name:        "精品导航",
		Version:     "1.3.18",
		Author:      "月言官方",
		Description: "精品站点导航：后台收藏管理（分类/标签/AI 智能分类/自动图标，开放/私有可见性），前台公开导航页 + 私有导航页（仅自己/密码访问），开放接口供浏览器插件同步。",
		Capabilities: []string{"api", "frontend", "settings", "data.read", "admin.page", "site.page"},
		Settings: []sdk.SettingField{
			{Key: "page_title", Label: "前台页面标题", Type: "text", Default: "精品导航"},
			{Key: "page_subtitle", Label: "前台页面副标题", Type: "text", Default: "收藏的优质站点"},
			{Key: "open_new_tab", Label: "新窗口打开站点", Type: "switch", Default: "on"},
		},
	}
}

// OnActivate 启用回调：创建存储并加载已有数据。
func (p *NavLinksPlugin) OnActivate(ctx context.Context) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	dir := filepath.Join(wd, "data", "plugins", pluginID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	store := NewLinkStore(dir)
	if err := store.Load(); err != nil {
		return err
	}
	priv := NewPrivateStore(dir)
	if err := priv.Load(); err != nil {
		return err
	}
	p.mu.Lock()
	p.store = store
	p.priv = priv
	p.mu.Unlock()
	return nil
}

// OnDeactivate 停用回调：释放存储（文件已随每次写操作落盘，无需额外保存）。
func (p *NavLinksPlugin) OnDeactivate(ctx context.Context) error {
	p.mu.Lock()
	p.store = nil
	p.priv = nil
	p.mu.Unlock()
	return nil
}

// Hooks 订阅钩子（本插件无钩子需求，数据与展示均走自定义 API）。
func (p *NavLinksPlugin) Hooks() []sdk.Hook { return nil }

// storeSafe 取当前链接存储（未激活时返回 nil，调用方判空）。
func (p *NavLinksPlugin) storeSafe() *LinkStore {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.store
}

// privSafe 取当前私有访问配置存储（未激活时返回 nil，调用方判空）。
func (p *NavLinksPlugin) privSafe() *PrivateStore {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.priv
}

// jsonResp 便捷构造 JSON 响应（纯函数）。
func jsonResp(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}

// publicSettings 组装前台公开配置（sdk.Config 快照 → 透传给公开页；纯函数）。
func publicSettings(cfg map[string]string) map[string]string {
	title := cfg["page_title"]
	if title == "" {
		title = "精品导航"
	}
	subtitle := cfg["page_subtitle"]
	if subtitle == "" {
		subtitle = "收藏的优质站点"
	}
	openTab := cfg["open_new_tab"]
	if openTab == "" {
		openTab = "on"
	}
	return map[string]string{"page_title": title, "page_subtitle": subtitle, "open_new_tab": openTab}
}

// RegisterAPI 自定义 API：管理端 CRUD + AI 建议 + 图标抓取 + 公开数据端点。
// 统一经宿主代理 /api/v1/plugins/nav-links/**（登录用户可用；写操作另校验管理员）。
// 注意：代理转发的 path 不含 query 参数，带参接口一律 POST + JSON body。
func (p *NavLinksPlugin) RegisterAPI(api *sdk.APIMux) {
	// 状态探针（手动验证代理链路）
	api.Handle("GET", "/status", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		count := 0
		if st := p.storeSafe(); st != nil {
			count = len(st.List())
		}
		return 200, jsonResp(map[string]any{"ok": true, "plugin": pluginID, "count": count}), nil
	})

	// 全量列表（管理页一次拉取：链接 + 管理口径分类/标签——手动列表 ∪ 条目聚合）
	api.Handle("GET", "/links", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		st := p.storeSafe()
		if st == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		return 200, jsonResp(map[string]any{"links": st.List(), "categories": st.CategoryList(), "tags": st.TagList()}), nil
	})

	// 新增站点（body: {url,name,category,tags,description,icon}）
	api.Handle("POST", "/links", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), nil
		}
		st := p.storeSafe()
		if st == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		in, err := parseLinkInput(body)
		if err != nil {
			return 400, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		link, err := st.Add(in)
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		return 200, jsonResp(map[string]any{"link": link}), nil
	})

	// 更新站点（body: {id,url,name,category,tags,description,icon}）
	api.Handle("POST", "/links/update", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), nil
		}
		st := p.storeSafe()
		if st == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		id, in, err := parseLinkUpdateInput(body)
		if err != nil {
			return 400, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		link, found, err := st.Update(id, in)
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		if !found {
			return 404, jsonResp(map[string]any{"error": "站点不存在（可能已被删除）"}), nil
		}
		return 200, jsonResp(map[string]any{"link": link}), nil
	})

	// 删除站点（body: {id}）
	api.Handle("POST", "/links/delete", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), nil
		}
		st := p.storeSafe()
		if st == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.ID <= 0 {
			return 400, jsonResp(map[string]any{"error": "缺少站点 id"}), nil
		}
		if !st.Delete(req.ID) {
			return 404, jsonResp(map[string]any{"error": "站点不存在（可能已被删除）"}), nil
		}
		return 200, jsonResp(map[string]any{"ok": true}), nil
	})

	// 手动排序（body: {ids: [3,1,2]}——按数组顺序重排）
	api.Handle("POST", "/links/reorder", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), nil
		}
		st := p.storeSafe()
		if st == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			IDs []int64 `json:"ids"`
		}
		if err := json.Unmarshal(body, &req); err != nil || len(req.IDs) == 0 {
			return 400, jsonResp(map[string]any{"error": "缺少排序 ids"}), nil
		}
		st.Reorder(req.IDs)
		return 200, jsonResp(map[string]any{"ok": true}), nil
	})

	// 公开数据端点（宿主桥接以系统身份调用：前台导航页 / 开放网关共用；
	// 仅返回开放条目——私有条目只经 /private/links 门禁通道外发，聚合分类/标签同步只算开放条目）
	api.Handle("POST", "/links/public", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		st := p.storeSafe()
		if st == nil {
			return 200, jsonResp(map[string]any{"links": []any{}, "categories": []string{}, "tags": []string{}, "settings": publicSettings(sdk.Config(ctx))}), nil
		}
		links := st.ListPublic()
		return 200, jsonResp(map[string]any{
			"links":      links,
			"categories": aggregateCategories(links),
			"tags":       aggregateTags(links),
			"settings":   publicSettings(sdk.Config(ctx)),
		}), nil
	})

	// 自动抓取站点图标（body: {url}；返回 dataURL——前端直接展示，不依赖外部资源）
	api.Handle("POST", "/fetch-icon", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		var req struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.URL == "" {
			return 400, jsonResp(map[string]any{"error": "缺少 url"}), nil
		}
		dataURL, source, err := fetchFavicon(req.URL)
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		return 200, jsonResp(map[string]any{"icon": dataURL, "source": source}), nil
	})

	// 批量补抓图标（body: {ids:[…]}；实现拆分在 icons.go——并发抓取 + 单条 8s 预算，
	// 适配宿主代理 10s 超时；管理页「批量补图标」分批循环调用）
	registerIconsAPI(api, p)

	// AI 模型列表（照抄 seo-optimizer 模式：无配置返回空，前端提示跳转配置）
	api.Handle("GET", "/ai/models", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		svc := sdk.Data(ctx)
		if svc == nil {
			return 200, jsonResp(map[string]any{"models": []any{}, "configured": false}), nil
		}
		models, err := svc.GetAIModels(ctx)
		if err != nil {
			return 200, jsonResp(map[string]any{"models": []any{}, "configured": false}), nil
		}
		return 200, jsonResp(map[string]any{"models": models, "configured": len(models) > 0}), nil
	})

	// AI 智能分类+标签（body: {url,name,description,model}）
	api.Handle("POST", "/ai/suggest", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		svc := sdk.Data(ctx)
		if svc == nil {
			return 200, jsonResp(map[string]any{"error": "AI 服务未授权（需 data.read 能力）"}), nil
		}
		model, in, err := parseAISuggestInput(body)
		if err != nil {
			return 400, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		result, err := suggestViaAI(ctx, svc, model, in)
		if err != nil {
			return 200, jsonResp(map[string]any{"error": err.Error()}), nil
		}
		return 200, jsonResp(result), nil
	})

	// 导航同步写入（宿主开放网关 navlinks.save 桥接调用，系统身份）：
	// 浏览器插件凭 Key 批量同步收藏；按地址 upsert，返回 {added, updated}。
	api.Handle("POST", "/links/import", func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
		if !sdk.TrustedCaller(ctx) {
			return 403, jsonResp(map[string]any{"error": "仅系统桥接或管理员可调用"}), nil
		}
		st := p.storeSafe()
		if st == nil {
			return 500, jsonResp(map[string]any{"error": "插件未激活"}), nil
		}
		var req struct {
			Links []LinkInput `json:"links"`
		}
		if err := json.Unmarshal(body, &req); err != nil || len(req.Links) == 0 {
			return 400, jsonResp(map[string]any{"error": "请求体需为 {\"links\":[{url,name,category,...}]} 且至少一条"}), nil
		}
		if len(req.Links) > 200 {
			return 400, jsonResp(map[string]any{"error": "单次最多导入 200 条"}), nil
		}
		items := make([]LinkInput, len(req.Links))
		for i, raw := range req.Links {
			raw.Icon = resolveImportIcon(raw.Icon)
			items[i] = raw
		}
		added, updated, err := st.ImportLinks(items)
		if err != nil {
			return 400, jsonResp(map[string]any{"error": err.Error(), "added": added, "updated": updated}), nil
		}
		return 200, jsonResp(map[string]any{"added": added, "updated": updated}), nil
	})

	// 分类/标签独立管理（增/重命名/删除级联）
	registerTaxonomyAPI(api, p)

	// 私有导航门禁（配置/元数据/解锁/私有数据）
	registerPrivateAPI(api, p)
}

// resolveImportIcon 导入条目的图标字段转换（纯流程函数）：
// http(s) 地址现场抓取转 dataURL（失败降级为空，前台首字母色块兜底）；
// dataURL 原样返回；空串原样返回。
func resolveImportIcon(icon string) string {
	icon = strings.TrimSpace(icon)
	if strings.HasPrefix(icon, "http://") || strings.HasPrefix(icon, "https://") {
		if dataURL, ok := downloadIconAsDataURL(icon); ok {
			return dataURL
		}
		return ""
	}
	return icon
}

// main 插件进程入口。
func main() {
	fmt.Fprintln(os.Stderr, "[nav-links] 进程启动")
	server.Serve(&NavLinksPlugin{})
}
