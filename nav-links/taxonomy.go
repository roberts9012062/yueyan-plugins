// nav-links/taxonomy.go
// 分类与标签：聚合/并集查询 + 独立管理（增/重命名/删除级联条目）+ 管理 API。
// 展示口径：前台只看条目聚合（Categories/AllTags）；管理端看「聚合 ∪ 手动列表」
// （CategoryList/TagList，使用中的在前、纯预置的在后）。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/roberts9012062/boke/pkg/plugin-sdk"
)

// ---------- 聚合与并集（纯函数） ----------

// aggregateCategories 从条目聚合分类（去空、去重、保首现序）。
func aggregateCategories(links []NavLink) []string {
	seen := make(map[string]bool)
	out := make([]string, 0)
	for _, l := range links {
		if l.Category == "" || seen[l.Category] {
			continue
		}
		seen[l.Category] = true
		out = append(out, l.Category)
	}
	return out
}

// aggregateTags 从条目聚合标签（去空、去重、保首现序）。
func aggregateTags(links []NavLink) []string {
	seen := make(map[string]bool)
	out := make([]string, 0)
	for _, l := range links {
		for _, t := range l.Tags {
			if t == "" || seen[t] {
				continue
			}
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// mergeUnique 并集：base 优先保序，补充 extra 中新出现的值。
func mergeUnique(base []string, extra []string) []string {
	seen := make(map[string]bool, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, v := range base {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, v := range extra {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// CategoryList 管理端分类：条目聚合 ∪ 手动管理列表（纯读）。
func (s *LinkStore) CategoryList() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return mergeUnique(aggregateCategories(s.links), s.categories)
}

// TagList 管理端标签：条目聚合 ∪ 手动管理列表（纯读）。
func (s *LinkStore) TagList() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return mergeUnique(aggregateTags(s.links), s.tags)
}

// ---------- 名称校验（纯函数） ----------

// validateTaxonomyName 校验分类/标签名称（返回规范化名称或错误）。
func validateTaxonomyName(raw string, maxLen int, label string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("请填写" + label + "名称")
	}
	if utf8.RuneCountInString(name) > maxLen {
		return "", errors.New(label + "名称不能超过 " + strconv.Itoa(maxLen) + " 字")
	}
	return name, nil
}

// containsString 判断切片是否含值（纯函数）。
func containsString(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// removeString 移除全部等于 v 的元素（纯函数，返回新切片）。
func removeString(list []string, v string) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		if item != v {
			out = append(out, item)
		}
	}
	return out
}

// sliceHas 判断是否有条目使用该分类（纯函数）。
func sliceHas(links []NavLink, category string) bool {
	for _, l := range links {
		if l.Category == category {
			return true
		}
	}
	return false
}

// ---------- 分类管理（级联条目） ----------

// AddCategory 新增分类到管理列表（查重；不关联条目）。
func (s *LinkStore) AddCategory(raw string) error {
	name, err := validateTaxonomyName(raw, linkCatMaxLen, "分类")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if containsString(s.categories, name) {
		return errors.New("分类「" + name + "」已存在")
	}
	s.categories = append(s.categories, name)
	return s.saveLocked()
}

// RenameCategory 重命名分类：列表改名 + 全部条目级联（to 已存在时合并归一）。
// 返回受影响条目数。
func (s *LinkStore) RenameCategory(rawFrom string, rawTo string) (int, error) {
	from, to, err := parseTaxPair(rawFrom, rawTo, linkCatMaxLen, "分类")
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 存在性检查（级联前）：管理列表或任一条目使用中即视为存在
	if !containsString(s.categories, from) && !sliceHas(s.links, from) {
		return 0, errors.New("分类「" + from + "」不存在")
	}
	affected := 0
	for i := range s.links {
		if s.links[i].Category == from {
			s.links[i].Category = to
			affected++
		}
	}
	s.categories = mergeUnique(removeString(s.categories, from), []string{to})
	if err := s.saveLocked(); err != nil {
		return 0, err
	}
	return affected, nil
}

// DeleteCategory 删除分类：列表移除 + 该分类下条目置为未分类。返回受影响条目数。
func (s *LinkStore) DeleteCategory(raw string) (int, error) {
	name, err := validateTaxonomyName(raw, linkCatMaxLen, "分类")
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	inList := containsString(s.categories, name)
	affected := 0
	for i := range s.links {
		if s.links[i].Category == name {
			s.links[i].Category = ""
			affected++
		}
	}
	if !inList && affected == 0 {
		return 0, errors.New("分类「" + name + "」不存在")
	}
	s.categories = removeString(s.categories, name)
	if err := s.saveLocked(); err != nil {
		return 0, err
	}
	return affected, nil
}

// ---------- 标签管理（级联条目） ----------

// AddTag 新增标签到管理列表（查重；不关联条目）。
func (s *LinkStore) AddTag(raw string) error {
	name, err := validateTaxonomyName(raw, linkTagItemMaxLen, "标签")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if containsString(s.tags, name) {
		return errors.New("标签「" + name + "」已存在")
	}
	s.tags = append(s.tags, name)
	return s.saveLocked()
}

// RenameTag 重命名标签：列表改名 + 全部条目标签级联替换（重名去重合并）。返回受影响条目数。
func (s *LinkStore) RenameTag(rawFrom string, rawTo string) (int, error) {
	from, to, err := parseTaxPair(rawFrom, rawTo, linkTagItemMaxLen, "标签")
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	inList := containsString(s.tags, from)
	affected := 0
	for i := range s.links {
		replaced := false
		cleaned := make([]string, 0, len(s.links[i].Tags))
		for _, t := range s.links[i].Tags {
			if t == from {
				replaced = true // 命中旧名改存新名（去重由下方 contains 保证）
				continue
			}
			if !containsString(cleaned, t) {
				cleaned = append(cleaned, t)
			}
		}
		if replaced {
			if !containsString(cleaned, to) {
				cleaned = append(cleaned, to)
			}
			s.links[i].Tags = cleaned
			affected++
		}
	}
	if !inList && affected == 0 {
		return 0, errors.New("标签「" + from + "」不存在")
	}
	s.tags = mergeUnique(removeString(s.tags, from), []string{to})
	if err := s.saveLocked(); err != nil {
		return 0, err
	}
	return affected, nil
}

// DeleteTag 删除标签：列表移除 + 全部条目移除该标签。返回受影响条目数。
func (s *LinkStore) DeleteTag(raw string) (int, error) {
	name, err := validateTaxonomyName(raw, linkTagItemMaxLen, "标签")
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	inList := containsString(s.tags, name)
	affected := 0
	for i := range s.links {
		if containsString(s.links[i].Tags, name) {
			s.links[i].Tags = removeString(s.links[i].Tags, name)
			affected++
		}
	}
	if !inList && affected == 0 {
		return 0, errors.New("标签「" + name + "」不存在")
	}
	s.tags = removeString(s.tags, name)
	if err := s.saveLocked(); err != nil {
		return 0, err
	}
	return affected, nil
}

// parseTaxPair 校验重命名的原/新名称对（纯函数）。
func parseTaxPair(rawFrom string, rawTo string, maxLen int, label string) (string, string, error) {
	from, err := validateTaxonomyName(rawFrom, maxLen, label)
	if err != nil {
		return "", "", err
	}
	to, err := validateTaxonomyName(rawTo, maxLen, label)
	if err != nil {
		return "", "", err
	}
	if from == to {
		return "", "", errors.New("新名称与原名称相同")
	}
	return from, to, nil
}

// ---------- 管理 API（统一守卫 + 表驱动六端点） ----------

// taxGuard 管理端点统一守卫：管理员鉴权 → 插件判活（失败返回响应与 false）。
func taxGuard(ctx context.Context, p *NavLinksPlugin) (*LinkStore, int, []byte, bool) {
	if !sdk.TrustedCaller(ctx) {
		return nil, 403, jsonResp(map[string]any{"error": "仅管理员可操作"}), false
	}
	st := p.storeSafe()
	if st == nil {
		return nil, 500, jsonResp(map[string]any{"error": "插件未激活"}), false
	}
	return st, 0, nil, true
}

// parseNameBody 解析 {name} 请求体（纯函数）。
func parseNameBody(body []byte) (string, string, error) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "", "", errors.New("请求体需为 JSON 对象")
	}
	return req.Name, "", nil
}

// parsePairBody 解析 {from,to} 请求体（纯函数）。
func parsePairBody(body []byte) (string, string, error) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "", "", errors.New("请求体需为 JSON 对象")
	}
	return req.From, req.To, nil
}

// taxEndpoint 管理端点描述：路径 + 请求解析 + 存储操作（统一返回受影响条目数）。
type taxEndpoint struct {
	Path  string                                                        // 端点路径（如 /categories）
	Parse func(body []byte) (string, string, error)                     // 请求体 → (a, b) 两参
	Run   func(st *LinkStore, a string, b string) (int, error)          // 存储操作
}

// registerTaxEndpoints 表驱动注册：守卫 → 解析 → 操作 → 响应 {ok, affected}。
func registerTaxEndpoints(api *sdk.APIMux, p *NavLinksPlugin, eps []taxEndpoint) {
	for _, ep := range eps {
		api.Handle("POST", ep.Path, func(ctx context.Context, method string, path string, body []byte) (int, []byte, error) {
			st, status, respBody, ok := taxGuard(ctx, p)
			if !ok {
				return status, respBody, nil
			}
			a, b, err := ep.Parse(body)
			if err != nil {
				return 400, jsonResp(map[string]any{"error": err.Error()}), nil
			}
			affected, err := ep.Run(st, a, b)
			if err != nil {
				return 200, jsonResp(map[string]any{"error": err.Error()}), nil
			}
			return 200, jsonResp(map[string]any{"ok": true, "affected": affected}), nil
		})
	}
}

// registerTaxonomyAPI 注册分类/标签管理端点（经宿主代理 /api/v1/plugins/nav-links/**）。
// 语义：重命名级联全部条目；删分类条目置未分类；删标签从条目移除；新增为预置（不关联条目）。
func registerTaxonomyAPI(api *sdk.APIMux, p *NavLinksPlugin) {
	registerTaxEndpoints(api, p, []taxEndpoint{
		{Path: "/categories", Parse: parseNameBody, Run: func(st *LinkStore, name string, unused string) (int, error) { return 0, st.AddCategory(name) }},
		{Path: "/categories/rename", Parse: parsePairBody, Run: func(st *LinkStore, from string, to string) (int, error) { return st.RenameCategory(from, to) }},
		{Path: "/categories/delete", Parse: parseNameBody, Run: func(st *LinkStore, name string, unused string) (int, error) { return st.DeleteCategory(name) }},
		{Path: "/tags", Parse: parseNameBody, Run: func(st *LinkStore, name string, unused string) (int, error) { return 0, st.AddTag(name) }},
		{Path: "/tags/rename", Parse: parsePairBody, Run: func(st *LinkStore, from string, to string) (int, error) { return st.RenameTag(from, to) }},
		{Path: "/tags/delete", Parse: parseNameBody, Run: func(st *LinkStore, name string, unused string) (int, error) { return st.DeleteTag(name) }},
	})
}
