// nav-links/private_test.go
// 私有导航功能测试：可见性分区（公开/私有）、导入不覆盖可见性、私有配置校验、
// 解锁 token 生命周期（签发/验证/密码轮换失效）、门禁 API 鉴权矩阵、公开端点过滤。
// 直接驱动 APIMux handler（System 身份注入），无需宿主进程桥。
package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
)

// newActivatedPlugin 构造已激活的插件实例（数据落在临时目录；t.Chdir 切工作目录）。
func newActivatedPlugin(t *testing.T) *NavLinksPlugin {
	t.Helper()
	t.Chdir(t.TempDir())
	p := &NavLinksPlugin{}
	if err := p.OnActivate(context.Background()); err != nil {
		t.Fatalf("激活失败：%v", err)
	}
	return p
}

// systemCtx 宿主桥接身份（System=true）。
func systemCtx() context.Context {
	return sdk.WithCallerIdentity(context.Background(), sdk.CallerIdentity{System: true})
}

// callJSON 调 mux handler 并解析 JSON 响应（纯测试辅助）。
func callJSON(t *testing.T, mux *sdk.APIMux, method string, path string, req any) (int, map[string]any) {
	t.Helper()
	handler := mux.Find(method, path)
	if handler == nil {
		t.Fatalf("路由未注册：%s %s", method, path)
	}
	body, _ := json.Marshal(req)
	status, raw, err := handler(systemCtx(), method, path, body)
	if err != nil {
		t.Fatalf("调用出错：%v", err)
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		t.Fatalf("响应非 JSON 对象：%s", string(raw))
	}
	return status, out
}

// TestVisibilityPartition 验证开放/私有条目在公开与私有视图的分区正确性。
func TestVisibilityPartition(t *testing.T) {
	p := newActivatedPlugin(t)
	st := p.storeSafe()
	if _, err := st.Add(LinkInput{URL: "https://open.com", Name: "开放站", Category: "工具"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Add(LinkInput{URL: "https://secret.com", Name: "私有站", Category: "工具", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Add(LinkInput{URL: "https://legacy.com", Name: "旧数据站", Category: "工具", Visibility: "open"}); err != nil {
		t.Fatal(err)
	}
	if got := len(st.List()); got != 3 {
		t.Fatalf("管理全量应为 3，实际 %d", got)
	}
	if got := len(st.ListPublic()); got != 2 {
		t.Fatalf("公开条目应为 2（默认开放+显式 open），实际 %d", got)
	}
	if got := len(st.ListPrivate()); got != 1 {
		t.Fatalf("私有条目应为 1，实际 %d", got)
	}
}

// TestNormalizeVisibility 验证可见性归一（非法值回退开放）。
func TestNormalizeVisibility(t *testing.T) {
	cases := map[string]string{
		"":         visibilityOpen,
		"open":     visibilityOpen,
		" private": visibilityPrivate,
		"hacked":   visibilityOpen,
	}
	for in, want := range cases {
		if got := normalizeVisibility(in); got != want {
			t.Fatalf("normalizeVisibility(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestLinkUpdateVisibility 验证编辑可切换可见性（开放 ↔ 私有）。
func TestLinkUpdateVisibility(t *testing.T) {
	p := newActivatedPlugin(t)
	st := p.storeSafe()
	link, err := st.Add(LinkInput{URL: "https://a.com", Name: "站", Category: "工具"})
	if err != nil {
		t.Fatal(err)
	}
	updated, found, err := st.Update(link.ID, LinkInput{URL: "https://a.com", Name: "站", Category: "工具", Visibility: "private"})
	if err != nil || !found {
		t.Fatalf("更新失败：%v found=%v", err, found)
	}
	if updated.Visibility != visibilityPrivate || len(st.ListPublic()) != 0 || len(st.ListPrivate()) != 1 {
		t.Fatal("更新为私有后分区不正确")
	}
}

// TestImportKeepsVisibility 验证导入 upsert：调用方未指定可见性时保留站点已有值。
func TestImportKeepsVisibility(t *testing.T) {
	p := newActivatedPlugin(t)
	st := p.storeSafe()
	if _, err := st.Add(LinkInput{URL: "https://keep.com", Name: "私有站", Category: "工具", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	added, updated, err := st.ImportLinks([]LinkInput{{URL: "https://keep.com", Name: "新名字", Category: "工具"}})
	if err != nil || added != 0 || updated != 1 {
		t.Fatalf("导入 upsert 失败：%v added=%d updated=%d", err, added, updated)
	}
	links := st.ListPrivate()
	if len(links) != 1 || links[0].Visibility != visibilityPrivate || links[0].Name != "新名字" {
		t.Fatal("空可见性导入覆盖了私有标记")
	}
}

// TestPrivateStoreFlow 验证私有配置：默认 self、密码校验、token 生命周期与密码轮换失效。
func TestPrivateStoreFlow(t *testing.T) {
	t.Chdir(t.TempDir())
	priv := NewPrivateStore(t.TempDir())
	if err := priv.Load(); err != nil {
		t.Fatal(err)
	}
	cfg := priv.Snapshot()
	if cfg.Mode != privateModeSelf {
		t.Fatalf("默认模式应为 self，实际 %s", cfg.Mode)
	}
	// 密码访问必须先有密码
	if err := priv.Update(privateModePassword, "", "标题", "副标题"); err == nil {
		t.Fatal("无密码切 password 模式应被拒绝")
	}
	// 密码过短
	if err := priv.Update(privateModePassword, "123", "标题", ""); err == nil {
		t.Fatal("短密码应被拒绝")
	}
	// 正确设置
	if err := priv.Update(privateModePassword, "hunter2secret", "我的私有导航", ""); err != nil {
		t.Fatalf("设置失败：%v", err)
	}
	if !priv.VerifyPassword("hunter2secret") || priv.VerifyPassword("wrong") {
		t.Fatal("密码校验不正确")
	}
	// token 签发与验证
	token, _ := priv.IssueToken()
	if !priv.VerifyToken(token) {
		t.Fatal("签发的 token 应有效")
	}
	if priv.VerifyToken(token + "x") || priv.VerifyToken("123.deadbeef") {
		t.Fatal("篡改 token 不应通过")
	}
	// 改密码 → secret 轮换 → 旧 token 失效
	if err := priv.Update(privateModePassword, "newpass99", "我的私有导航", ""); err != nil {
		t.Fatal(err)
	}
	if priv.VerifyToken(token) {
		t.Fatal("密码变更后旧 token 应失效")
	}
	if !priv.VerifyPassword("newpass99") {
		t.Fatal("新密码应生效")
	}
}

// TestPrivateAPIAccessMatrix 验证门禁 API 的完整鉴权矩阵。
func TestPrivateAPIAccessMatrix(t *testing.T) {
	p := newActivatedPlugin(t)
	st := p.storeSafe()
	mux := sdk.NewAPIMux()
	p.RegisterAPI(mux)
	if _, err := st.Add(LinkInput{URL: "https://sec.com", Name: "私有站", Category: "工具", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	priv := p.privSafe()

	// 初始 self 模式：匿名无凭证 → 403 self_only；unlock 亦被拒
	status, out := callJSON(t, mux, "POST", "/private/links", map[string]any{})
	if status != 403 || out["code"] != "self_only" {
		t.Fatalf("self 模式匿名应 403 self_only，实际 %d %v", status, out["code"])
	}
	status, out = callJSON(t, mux, "POST", "/private/unlock", map[string]any{"password": "x"})
	if status != 403 {
		t.Fatalf("self 模式 unlock 应 403，实际 %d", status)
	}
	// meta 公开元数据可用
	status, out = callJSON(t, mux, "POST", "/private/meta", map[string]any{})
	if status != 200 || out["mode"] != privateModeSelf || out["count"].(float64) != 1 {
		t.Fatalf("meta 应返回 self 模式与 1 条计数，实际 %d %v", status, out)
	}

	// 切 password 模式并设密码
	if err := priv.Update(privateModePassword, "unlock123", "私有导航", ""); err != nil {
		t.Fatal(err)
	}
	// 无 token → 401 need_password；错密码 → 401
	status, out = callJSON(t, mux, "POST", "/private/links", map[string]any{})
	if status != 401 || out["code"] != "need_password" {
		t.Fatalf("password 模式无凭证应 401 need_password，实际 %d %v", status, out["code"])
	}
	status, _ = callJSON(t, mux, "POST", "/private/unlock", map[string]any{"password": "bad"})
	if status != 401 {
		t.Fatalf("错误密码应 401，实际 %d", status)
	}
	// 正确密码 → token；带 token 取数成功且仅含私有条目
	status, out = callJSON(t, mux, "POST", "/private/unlock", map[string]any{"password": "unlock123"})
	if status != 200 || out["token"] == "" {
		t.Fatalf("正确密码应返回 token，实际 %d %v", status, out)
	}
	status, out = callJSON(t, mux, "POST", "/private/links", map[string]any{"token": out["token"]})
	if status != 200 {
		t.Fatalf("有效 token 应放行，实际 %d", status)
	}
	if links, _ := out["links"].([]any); len(links) != 1 {
		t.Fatalf("私有数据应恰含 1 条，实际 %v", out["links"])
	}
	// 管理员身份直通（无需 token）
	status, _ = callJSON(t, mux, "POST", "/private/links", map[string]any{"admin": true})
	if status != 200 {
		t.Fatalf("admin 应直通，实际 %d", status)
	}
}

// TestPublicEndpointExcludesPrivate 验证 /links/public 只输出开放条目与聚合。
func TestPublicEndpointExcludesPrivate(t *testing.T) {
	p := newActivatedPlugin(t)
	st := p.storeSafe()
	if _, err := st.Add(LinkInput{URL: "https://a.com", Name: "开放站", Category: "公开类", Tags: []string{"t1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Add(LinkInput{URL: "https://b.com", Name: "私有站", Category: "私密类", Tags: []string{"t2"}, Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	status, out := callJSON(t, p.apiMuxForTest(t), "POST", "/links/public", map[string]any{})
	if status != 200 {
		t.Fatalf("公开端点应 200，实际 %d", status)
	}
	if links, _ := out["links"].([]any); len(links) != 1 {
		t.Fatalf("公开端点应只含 1 条开放条目，实际 %v", out["links"])
	}
	if cats, _ := out["categories"].([]any); len(cats) != 1 || cats[0] != "公开类" {
		t.Fatalf("聚合分类应只含公开类，实际 %v", out["categories"])
	}
	if tags, _ := out["tags"].([]any); len(tags) != 1 || tags[0] != "t1" {
		t.Fatalf("聚合标签应只含 t1，实际 %v", out["tags"])
	}
}

// apiMuxForTest 注册全部 API 并返回 mux（TestPublicEndpointExcludesPrivate 用）。
func (p *NavLinksPlugin) apiMuxForTest(t *testing.T) *sdk.APIMux {
	t.Helper()
	mux := sdk.NewAPIMux()
	p.RegisterAPI(mux)
	return mux
}
