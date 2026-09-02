// nav-links/store.go
// 精品导航数据存储：插件数据目录 links.json（JSON 文件 + 互斥锁 + 临时文件原子替换）。
// 数据模型与校验收敛于此；main.go 的 API 处理器只做参数解析与调用。
package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// 字段长度上限（与前端表单提示一致）。
const (
	linkNameMaxLen    = 60  // 站点名称上限（字符）
	linkCatMaxLen     = 30  // 分类上限（字符）
	linkDescMaxLen    = 200 // 简介上限（字符）
	linkTagItemMaxLen = 20  // 单个标签上限（字符）
	linkTagMaxCount   = 10  // 标签数量上限
	linkIconMaxRunes  = 140 * 1024 // 图标 dataURL 字符上限（约 100KB 二进制，抓取侧另限 64KB）
)

// 可见性取值（v1.3.14 起）：空与 open 均为开放（旧数据无字段天然兼容），private 为私有。
const (
	visibilityOpen    = ""         // 开放（默认；省存储按空串存储）
	visibilityPrivate = "private"  // 私有（仅私有导航页展示，公开页不出现）
)

// NavLink 导航条目（存储结构；Icon 为 dataURL 内嵌，前台展示不依赖外部网络资源）。
type NavLink struct {
	ID          int64    `json:"id"`                  // 站点 ID（自增）
	URL         string   `json:"url"`                 // 站点地址（http/https）
	Name        string   `json:"name"`                // 网站名字
	Category    string   `json:"category"`            // 分类（必填）
	Tags        []string `json:"tags"`                // 标签（可选，≤10 个）
	Description string   `json:"description"`         // 站点简介
	Icon        string   `json:"icon"`                // 图标 dataURL（空=前端首字母色块占位）
	Visibility  string   `json:"visibility,omitempty"` // 可见性（空=开放；private=私有）
	Sort        int      `json:"sort"`                // 手动排序序号（小在前）
	CreatedAt   string   `json:"created_at"`          // 收藏时间（RFC3339）
}

// LinkInput 新增/更新入参（表单字段，校验前后的同构载体）。
type LinkInput struct {
	URL         string   `json:"url"`
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
	Description string   `json:"description"`
	Icon        string   `json:"icon"`
	Visibility  string   `json:"visibility"` // 空/open=开放（默认）；private=私有
}

// LinkStore JSON 文件存储（文件系统连接器：互斥锁保护，临时文件 + rename 原子写）。
type LinkStore struct {
	mu         sync.Mutex
	path       string    // links.json 绝对路径
	links      []NavLink // 全量条目
	categories []string  // 手动管理的分类列表（与条目聚合取并集对外呈现）
	tags       []string  // 手动管理的标签列表（同上）
	nextID     int64     // 下一个自增 ID
}

// storeData 落盘格式（v1.3.7 起：对象；旧版为裸数组，Load 时自动迁移）。
type storeData struct {
	Links      []NavLink `json:"links"`
	Categories []string  `json:"categories"`
	Tags       []string  `json:"tags"`
}

// NewLinkStore 创建存储（dir 为插件数据目录；Load 前为空存储）。
func NewLinkStore(dir string) *LinkStore {
	return &LinkStore{
		path:       filepath.Join(dir, "links.json"),
		links:      make([]NavLink, 0),
		categories: make([]string, 0),
		tags:       make([]string, 0),
		nextID:     1,
	}
}

// Load 从磁盘加载（文件不存在视为空存储；数据损坏返回错误阻断激活，避免静默覆盖）。
// 兼容 v1.3.5/1.3.6 的裸数组格式：识别后迁移为对象格式并把分类/标签聚合进管理列表。
func (s *LinkStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	// 探测格式：对象格式首字符为 {（含 links/categories/tags 键），旧数组格式为 [
		var data storeData
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal(raw, &data); err != nil {
			return errors.New("links.json 数据损坏：" + err.Error())
		}
	} else {
		var legacy []NavLink
		if err := json.Unmarshal(raw, &legacy); err != nil {
			return errors.New("links.json 数据损坏：" + err.Error())
		}
		data.Links = legacy
		// 旧格式迁移：条目中的分类/标签全部升格为管理列表（聚合去重保序）
		data.Categories = aggregateCategories(legacy)
		data.Tags = aggregateTags(legacy)
	}
	if data.Links == nil {
		data.Links = make([]NavLink, 0)
	}
	if data.Categories == nil {
		data.Categories = make([]string, 0)
	}
	if data.Tags == nil {
		data.Tags = make([]string, 0)
	}
	s.links = data.Links
	s.categories = data.Categories
	s.tags = data.Tags
	s.nextID = 1
	for _, l := range s.links {
		if l.ID >= s.nextID {
			s.nextID = l.ID + 1
		}
	}
	return nil
}

// saveLocked 落盘（调用方须已持锁；写临时文件后原子替换）。
func (s *LinkStore) saveLocked() error {
	raw, err := json.MarshalIndent(storeData{Links: s.links, Categories: s.categories, Tags: s.tags}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// List 返回全量条目拷贝（按 Sort 升序，同序按 ID 升序；新收藏自然排到分类末尾）。
func (s *LinkStore) List() []NavLink {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]NavLink, len(s.links))
	copy(out, s.links)
	sort.SliceStable(out, func(i int, j int) bool {
		if out[i].Sort != out[j].Sort {
			return out[i].Sort < out[j].Sort
		}
		return out[i].ID < out[j].ID
	})
	for i := range out {
		out[i].Tags = append([]string(nil), out[i].Tags...) // 拷贝切片，防外部改内部
	}
	return out
}

// ListPublic 返回开放条目拷贝（前台公开页数据源：私有条目不外泄；排序规则同 List）。
func (s *LinkStore) ListPublic() []NavLink {
	all := s.List()
	out := make([]NavLink, 0, len(all))
	for _, l := range all {
		if l.Visibility != visibilityPrivate {
			out = append(out, l)
		}
	}
	return out
}

// ListPrivate 返回私有条目拷贝（私有导航页数据源；排序规则同 List）。
func (s *LinkStore) ListPrivate() []NavLink {
	all := s.List()
	out := make([]NavLink, 0, len(all))
	for _, l := range all {
		if l.Visibility == visibilityPrivate {
			out = append(out, l)
		}
	}
	return out
}

// Categories 前台展示用分类：条目聚合（只含有站点使用的分类；纯读）。
func (s *LinkStore) Categories() []string {
	s.mu.Lock()
	links := make([]NavLink, len(s.links))
	copy(links, s.links)
	s.mu.Unlock()
	return aggregateCategories(links)
}

// AllTags 前台展示用标签：条目聚合（只含有站点使用的标签；纯读）。
func (s *LinkStore) AllTags() []string {
	s.mu.Lock()
	links := make([]NavLink, len(s.links))
	copy(links, s.links)
	s.mu.Unlock()
	return aggregateTags(links)
}

// Add 新增站点（校验入参 + URL 查重；Sort 取当前最大值 +1）。
func (s *LinkStore) Add(in LinkInput) (NavLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.links {
		if strings.EqualFold(l.URL, in.URL) {
			return NavLink{}, errors.New("该地址已收藏：" + l.Name)
		}
	}
	maxSort := 0
	for _, l := range s.links {
		if l.Sort > maxSort {
			maxSort = l.Sort
		}
	}
	link := NavLink{
		ID:          s.nextID,
		URL:         in.URL,
		Name:        in.Name,
		Category:    in.Category,
		Tags:        in.Tags,
		Description: in.Description,
		Icon:        in.Icon,
		Visibility:  in.Visibility,
		Sort:        maxSort + 1,
		CreatedAt:   time.Now().Format(time.RFC3339),
	}
	s.nextID++
	s.links = append(s.links, link)
	if err := s.saveLocked(); err != nil {
		return NavLink{}, errors.New("保存失败：" + err.Error())
	}
	return link, nil
}

// Update 更新站点（按 ID 查找；URL 查重排除自身）。
func (s *LinkStore) Update(id int64, in LinkInput) (NavLink, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, l := range s.links {
		if l.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return NavLink{}, false, nil
	}
	for i, l := range s.links {
		if i != idx && strings.EqualFold(l.URL, in.URL) {
			return NavLink{}, true, errors.New("该地址已被「" + l.Name + "」收藏")
		}
	}
	s.links[idx].URL = in.URL
	s.links[idx].Name = in.Name
	s.links[idx].Category = in.Category
	s.links[idx].Tags = in.Tags
	s.links[idx].Description = in.Description
	s.links[idx].Icon = in.Icon
	s.links[idx].Visibility = in.Visibility
	if err := s.saveLocked(); err != nil {
		return NavLink{}, true, errors.New("保存失败：" + err.Error())
	}
	return s.links[idx], true, nil
}

// Delete 删除站点（不存在返回 false）。
func (s *LinkStore) Delete(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, l := range s.links {
		if l.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}
	s.links = append(s.links[:idx], s.links[idx+1:]...)
	_ = s.saveLocked() // 删除为幂等意图，落盘失败不影响内存一致性（下次写会重试）
	return true
}

// Reorder 按给定 ID 顺序重排 Sort（未出现在列表中的条目保持相对顺序排在末尾）。
func (s *LinkStore) Reorder(ids []int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pos := make(map[int64]int, len(ids))
	for i, id := range ids {
		pos[id] = i
	}
	sort.SliceStable(s.links, func(i int, j int) bool {
		pi, oki := pos[s.links[i].ID]
		pj, okj := pos[s.links[j].ID]
		if oki && okj {
			return pi < pj
		}
		if oki != okj {
			return oki // 命中列表的排前面
		}
		return s.links[i].ID < s.links[j].ID // 双双未命中按 ID 稳定收尾
	})
	for i := range s.links {
		s.links[i].Sort = i + 1
	}
	_ = s.saveLocked()
}

// ImportLinks 批量导入站点（浏览器插件「导航同步写入」通道；纯函数式 upsert）。
// 按 URL（规范化后）匹配：已存在则更新内容字段（已有图标不被空图标覆盖），不存在则追加。
// 返回：新增数、更新数；任一条目校验失败即中止（调用方先整批校验以保证原子性提示）。
func (s *LinkStore) ImportLinks(items []LinkInput) (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	added := 0
	updated := 0
	maxSort := 0
	for _, l := range s.links {
		if l.Sort > maxSort {
			maxSort = l.Sort
		}
	}
	for _, raw := range items {
		in, err := validateLinkInput(raw)
		if err != nil {
			return added, updated, err
		}
		idx := -1
		for i, l := range s.links {
			if strings.EqualFold(l.URL, in.URL) {
				idx = i
				break
			}
		}
		if idx >= 0 {
			s.links[idx].Name = in.Name
			s.links[idx].Category = in.Category
			s.links[idx].Tags = in.Tags
			s.links[idx].Description = in.Description
			// 可见性为空表示调用方未指定（浏览器插件同步默认开放口径）——
			// 保留站点已有值，避免批量同步把站长手工标记的私有条目刷回开放
			if in.Visibility != visibilityOpen {
				s.links[idx].Visibility = in.Visibility
			}
			if in.Icon != "" {
				s.links[idx].Icon = in.Icon
			}
			updated++
			continue
		}
		maxSort++
		s.links = append(s.links, NavLink{
			ID:          s.nextID,
			URL:         in.URL,
			Name:        in.Name,
			Category:    in.Category,
			Tags:        in.Tags,
			Description: in.Description,
			Icon:        in.Icon,
			Visibility:  in.Visibility,
			Sort:        maxSort,
			CreatedAt:   time.Now().Format(time.RFC3339),
		})
		s.nextID++
		added++
	}
	if err := s.saveLocked(); err != nil {
		return added, updated, errors.New("保存失败：" + err.Error())
	}
	return added, updated, nil
}

// normalizeURL 规范化地址：无 scheme 时补 https://，去除首尾空白与末尾斜杠（纯函数）。
// 末尾斜杠归一保证「https://a.com」与「https://a.com/」视为同一站点（查重与 upsert 匹配一致）。
func normalizeURL(raw string) string {
	u := strings.TrimSpace(raw)
	if u == "" {
		return ""
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "https://" + u
	}
	return strings.TrimSuffix(u, "/")
}

// cleanTags 清洗标签列表：去空白、去空、去重、截长度、限数量（纯函数）。
func cleanTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := make(map[string]bool)
	for _, t := range tags {
		v := strings.TrimSpace(t)
		if v == "" || seen[v] {
			continue
		}
		if utf8.RuneCountInString(v) > linkTagItemMaxLen {
			r := []rune(v)
			v = string(r[:linkTagItemMaxLen])
		}
		seen[v] = true
		out = append(out, v)
		if len(out) >= linkTagMaxCount {
			break
		}
	}
	return out
}

// validateLinkInput 校验并规范化表单入参（纯函数；返回清洗后的输入或错误）。
func validateLinkInput(in LinkInput) (LinkInput, error) {
	out := LinkInput{
		URL:         normalizeURL(in.URL),
		Name:        strings.TrimSpace(in.Name),
		Category:    strings.TrimSpace(in.Category),
		Tags:        cleanTags(in.Tags),
		Description: strings.TrimSpace(in.Description),
		Icon:        strings.TrimSpace(in.Icon),
		Visibility:  normalizeVisibility(in.Visibility),
	}
	if out.URL == "" {
		return LinkInput{}, errors.New("请填写站点地址")
	}
	parsed, err := url.Parse(out.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return LinkInput{}, errors.New("站点地址格式不正确（需 http/https）")
	}
	if out.Name == "" {
		return LinkInput{}, errors.New("请填写网站名字")
	}
	if utf8.RuneCountInString(out.Name) > linkNameMaxLen {
		return LinkInput{}, errors.New("网站名字不能超过 60 字")
	}
	if out.Category == "" {
		return LinkInput{}, errors.New("请填写分类（或点击 AI 智能分类）")
	}
	if utf8.RuneCountInString(out.Category) > linkCatMaxLen {
		return LinkInput{}, errors.New("分类不能超过 30 字")
	}
	if utf8.RuneCountInString(out.Description) > linkDescMaxLen {
		return LinkInput{}, errors.New("站点简介不能超过 200 字")
	}
	if out.Icon != "" && (!strings.HasPrefix(out.Icon, "data:image/") || len(out.Icon) > linkIconMaxRunes) {
		return LinkInput{}, errors.New("图标格式不正确（需 data:image/* 且不超过 100KB）")
	}
	return out, nil
}

// normalizeVisibility 可见性归一（纯函数）：空/open → 空（开放）；private → private；
// 其余非法值一律回退开放（保守拒绝而非报错——同步通道的宽容口径）。
func normalizeVisibility(raw string) string {
	v := strings.TrimSpace(raw)
	if v == visibilityPrivate {
		return visibilityPrivate
	}
	return visibilityOpen
}

// parseLinkInput 解析新增请求 body（纯函数）。
func parseLinkInput(body []byte) (LinkInput, error) {
	var in LinkInput
	if err := json.Unmarshal(body, &in); err != nil {
		return LinkInput{}, errors.New("请求体需为 JSON 对象")
	}
	return validateLinkInput(in)
}

// parseLinkUpdateInput 解析更新请求 body（平铺：id + 表单字段；纯函数）。
func parseLinkUpdateInput(body []byte) (int64, LinkInput, error) {
	var req struct {
		ID          int64    `json:"id"`
		URL         string   `json:"url"`
		Name        string   `json:"name"`
		Category    string   `json:"category"`
		Tags        []string `json:"tags"`
		Description string   `json:"description"`
		Icon        string   `json:"icon"`
		Visibility  string   `json:"visibility"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return 0, LinkInput{}, errors.New("请求体需为 JSON 对象")
	}
	if req.ID <= 0 {
		return 0, LinkInput{}, errors.New("缺少站点 id")
	}
	valid, err := validateLinkInput(LinkInput{
		URL:         req.URL,
		Name:        req.Name,
		Category:    req.Category,
		Tags:        req.Tags,
		Description: req.Description,
		Icon:        req.Icon,
		Visibility:  req.Visibility,
	})
	if err != nil {
		return 0, LinkInput{}, err
	}
	return req.ID, valid, nil
}
