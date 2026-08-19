// marketplace-repo/stats-pro/stats.go
// 计数存储：按日 PV/UV + 帖子浏览计数 + 累计总量；内存计数、节流落盘。
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// 落盘参数（节流间隔与热门榜容量）。
const (
	statsFlushInterval = 5 * time.Second // 节流落盘间隔（计数高频，批量写）
	statsTopLimit      = 10              // 热门内容榜单容量
)

// dayCount 单日计数（date 键为 2006-01-02）。
type dayCount struct {
	PV int `json:"pv"` // 页面浏览量
	UV int `json:"uv"` // 独立访客数（visitor_id 去重）
}

// statsData 计数持久化结构（stats.json）。
type statsData struct {
	Days     map[string]*dayCount `json:"days"`      // 日计数（近 90 天，落盘时裁剪）
	DayUV    map[string][]string  `json:"day_uv"`    // 日访客 ID 集合（落盘时清空——UV 去重只在当日进程内需要）
	Posts    map[string]int64     `json:"posts"`     // 帖子浏览计数（post_id → 次数）
	TotalPV  int64                `json:"total_pv"`  // 累计 PV
	TotalUV  int64                `json:"total_uv"`  // 累计 UV（按日累加的近似）
}

// statsStore 计数存储（并发安全：上报与查询可能同时进入）。
type statsStore struct {
	mu        sync.Mutex
	path      string
	data      statsData
	dirty     bool
	lastFlush time.Time
}

// newStatsStore 创建计数存储（目录 data/plugins/{id}/，加载历史数据）。
func newStatsStore(pluginID string) (*statsStore, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	base := filepath.Join(wd, "data", "plugins", pluginID)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, err
	}
	store := &statsStore{
		path: filepath.Join(base, "stats.json"),
		data: statsData{Days: map[string]*dayCount{}, DayUV: map[string][]string{}, Posts: map[string]int64{}},
	}
	if raw, err := os.ReadFile(store.path); err == nil {
		_ = json.Unmarshal(raw, &store.data) // 历史缺失字段兜底
		if store.data.Days == nil {
			store.data.Days = map[string]*dayCount{}
		}
		if store.data.Posts == nil {
			store.data.Posts = map[string]int64{}
		}
	}
	return store, nil
}

// record 记录一次访问：日期按当日、visitor_id 空串只计 PV；帖子 ID 非空再计帖子数。
func (s *statsStore) record(day string, visitorID string, postID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dayStats, ok := s.data.Days[day]
	if !ok {
		dayStats = &dayCount{}
		s.data.Days[day] = dayStats
	}
	dayStats.PV++
	s.data.TotalPV++
	if visitorID != "" && !containsID(s.data.DayUV[day], visitorID) {
		s.data.DayUV[day] = append(s.data.DayUV[day], visitorID)
		dayStats.UV++
		s.data.TotalUV++
	}
	if postID != "" {
		s.data.Posts[postID]++
	}
	s.dirty = true
	// 节流落盘（距上次 ≥ 间隔才写；停用回调兜底 flush）
	if time.Since(s.lastFlush) >= statsFlushInterval {
		s.writeLocked()
	}
}

// containsID 线性包含判定（单日访客量有限，避免引 map[string]map 双结构；纯函数）。
func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// topPosts 热门内容榜单（按浏览次数降序取前 N；纯读）。
func (s *statsStore) topPosts() []postHit {
	s.mu.Lock()
	defer s.mu.Unlock()
	hits := make([]postHit, 0, len(s.data.Posts))
	for id, count := range s.data.Posts {
		hits = append(hits, postHit{PostID: id, Hits: count})
	}
	sort.Slice(hits, func(i int, j int) bool { return hits[i].Hits > hits[j].Hits })
	if len(hits) > statsTopLimit {
		hits = hits[:statsTopLimit]
	}
	return hits
}

// dayStats 取某日计数（无记录返回零值）。
func (s *statsStore) dayStats(day string) dayCount {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.data.Days[day]; ok {
		return *d
	}
	return dayCount{}
}

// totals 取累计计数。
func (s *statsStore) totals() (int64, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.TotalPV, s.data.TotalUV
}

// flush 强制落盘（停用回调调用）。
func (s *statsStore) flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked()
}

// writeLocked 落盘（调用方需持锁）：裁剪 90 天外的日计数、清空 UV 明细后写文件。
func (s *statsStore) writeLocked() error {
	s.lastFlush = time.Now()
	out := statsData{Days: map[string]*dayCount{}, Posts: s.data.Posts, TotalPV: s.data.TotalPV, TotalUV: s.data.TotalUV}
	cutoff := time.Now().AddDate(0, 0, -90).Format("2006-01-02")
	for day, d := range s.data.Days {
		if day >= cutoff {
			out.Days[day] = d
		}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.path, raw, 0o644); err != nil {
		return err
	}
	s.dirty = false
	return nil
}
