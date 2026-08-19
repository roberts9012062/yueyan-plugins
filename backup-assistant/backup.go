// marketplace-repo/backup-assistant/backup.go
// 备份存储：媒体目录 ZIP 打包 + 历史索引 + 保留清理 + 状态记录 + 完成通知。
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 目录约定（相对宿主工作目录；与 tts-reader 等插件的 data/plugins/{id} 惯例一致）。
const (
	mediaDir       = "data/media"                                    // 站点媒体库（备份对象）
	stateFile      = "state.json"                                     // 调度状态（last_run）
	backupFilePrefix = "backup-"                                      // 备份文件名前缀
)

// backupItem 单条备份历史（索引条目，也直接作为 API 响应项）。
type backupItem struct {
	File      string `json:"file"`       // 文件名（backups 目录内）
	Size      int64  `json:"size"`       // 字节数
	CreatedAt string `json:"created_at"` // 备份时间（RFC3339）
}

// backupState 调度状态（磁盘持久化，重启不丢调度基准）。
type backupState struct {
	LastRun time.Time `json:"last_run"` // 上次成功备份时间
}

// backupStore 备份存储（并发安全：手动触发与定时任务可能同时进入）。
type backupStore struct {
	mu        sync.Mutex
	dir       string // 备份目录 data/plugins/{id}/backups
	indexPath string // 历史索引 data/plugins/{id}/backups/index.json
	statePath string // 调度状态 data/plugins/{id}/state.json
}

// newBackupStore 创建备份存储（建目录 + 容错加载/修复索引）。
func newBackupStore(pluginID string) (*backupStore, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	base := filepath.Join(wd, "data", "plugins", pluginID)
	dir := filepath.Join(base, "backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	store := &backupStore{
		dir:       dir,
		indexPath: filepath.Join(dir, "index.json"),
		statePath: filepath.Join(base, stateFile),
	}
	store.rebuildIndexIfNeeded()
	return store, nil
}

// runBackup 执行一次完整备份：打包媒体库 → 写索引 → 保留清理 → 记状态 → 通知。
// 返回本次备份条目（失败返回错误，不记状态——定时任务会重试）。
func (s *backupStore) runBackup(cfg map[string]string) (*backupItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.packMedia()
	if err != nil {
		return nil, err
	}
	items := s.loadIndex()
	items = append(items, *item)
	items = pruneBackups(s.dir, items, retentionCount(cfg))
	s.saveIndex(items)
	s.saveState(backupState{LastRun: time.Now()})
	notifyWebhook(cfg["webhook_url"], *item)
	return item, nil
}

// packMedia 打包媒体目录为 ZIP（相对路径保留目录结构；空目录产出空包属正常）。
func (s *backupStore) packMedia() (*backupItem, error) {
	media := mediaDir
	if _, err := os.Stat(media); err != nil {
		return nil, fmt.Errorf("媒体目录不存在（%s）：%w", media, err)
	}
	name := backupFilePrefix + time.Now().Format("20060102-150405") + ".zip"
	target := filepath.Join(s.dir, name)
	var buf bytes.Buffer
	if err := zipDir(&buf, media); err != nil {
		return nil, err
	}
	if err := os.WriteFile(target, buf.Bytes(), 0o644); err != nil {
		return nil, err
	}
	return &backupItem{File: name, Size: int64(buf.Len()), CreatedAt: time.Now().Format(time.RFC3339)}, nil
}

// zipDir 把目录递归打包进 ZIP（条目路径为目录内相对路径，zip 头统一 UTF-8 名）。
func zipDir(buf *bytes.Buffer, root string) error {
	w := zip.NewWriter(buf)
	defer w.Close()
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err // 目录由文件条目隐含，无需单独条目
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entry, err := w.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(entry, f)
		return err
	})
}

// loadIndex 读历史索引（损坏/不存在返回空表——历史可由文件重建，不阻断备份）。
func (s *backupStore) loadIndex() []backupItem {
	raw, err := os.ReadFile(s.indexPath)
	if err != nil {
		return nil
	}
	var items []backupItem
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	return items
}

// saveIndex 写历史索引。
func (s *backupStore) saveIndex(items []backupItem) {
	raw, _ := json.MarshalIndent(items, "", "  ")
	_ = os.WriteFile(s.indexPath, raw, 0o644)
}

// rebuildIndexIfNeeded 索引缺失/为空但目录有备份文件时按文件名重建（自愈）。
func (s *backupStore) rebuildIndexIfNeeded() {
	items := s.loadIndex()
	if len(items) > 0 {
		return
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	rebuilt := make([]backupItem, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, backupFilePrefix) || !strings.HasSuffix(name, ".zip") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		rebuilt = append(rebuilt, backupItem{File: name, Size: info.Size(), CreatedAt: info.ModTime().Format(time.RFC3339)})
	}
	if len(rebuilt) > 0 {
		s.saveIndex(rebuilt)
	}
}

// loadState 读调度状态（不存在返回零值）。
func (s *backupStore) loadState() (backupState, error) {
	raw, err := os.ReadFile(s.statePath)
	if err != nil {
		return backupState{}, err
	}
	var state backupState
	err = json.Unmarshal(raw, &state)
	return state, err
}

// saveState 写调度状态。
func (s *backupStore) saveState(state backupState) {
	raw, _ := json.Marshal(state)
	_ = os.WriteFile(s.statePath, raw, 0o644)
}

// history 备份历史（按时间倒序——最新在前）。
func (s *backupStore) history() []backupItem {
	items := s.loadIndex()
	sort.Slice(items, func(i int, j int) bool { return items[i].CreatedAt > items[j].CreatedAt })
	return items
}

// pruneBackups 保留策略：超出份数删最旧（文件 + 索引条目同删；纯函数返回新列表）。
func pruneBackups(dir string, items []backupItem, keep int) []backupItem {
	if keep <= 0 || len(items) <= keep {
		return items
	}
	sort.Slice(items, func(i int, j int) bool { return items[i].CreatedAt < items[j].CreatedAt })
	for _, old := range items[:len(items)-keep] {
		_ = os.Remove(filepath.Join(dir, old.File))
	}
	return items[len(items)-keep:]
}

// retentionCount 读保留份数（非法回退 5；纯函数）。
func retentionCount(cfg map[string]string) int {
	n, err := strconv.Atoi(strings.TrimSpace(cfg["retention_count"]))
	if err != nil || n <= 0 {
		return 5
	}
	return n
}

// notifyWebhook 备份完成通知（POST 元信息 JSON；10 秒超时，失败只留 stderr 不影响备份）。
func notifyWebhook(url string, item backupItem) {
	url = strings.TrimSpace(url)
	if url == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{"event": "backup.done", "file": item.File, "size": item.Size, "created_at": item.CreatedAt})
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "[backup-assistant] 通知发送失败：", err)
		return
	}
	_ = resp.Body.Close()
}
