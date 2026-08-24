// marketplace-repo/backup-assistant/backup.go
// 备份存储：多目标采集（数据库/媒体/前端/后端）→ 单 ZIP 流式落盘 + 历史索引
// + 保留清理 + 状态记录 + 完成通知。
//
// 部分失败语义：单一目标失败不阻断其余目标（失败原因记入 parts）；
// 所有启用目标全部失败才视为整体失败（删除半成品文件，定时任务下轮重试）。
package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
	mediaDir         = "data/media"    // 站点媒体库（备份对象）
	stateFile        = "state.json"    // 调度状态（last_run）
	backupFilePrefix = "backup-"       // 备份文件名前缀
	entryManifest    = "manifest.json" // 备份内容清单（ZIP 内条目）
)

// 备份部分状态枚举。
const (
	stateOK      = "ok"
	stateSkipped = "skipped"
	stateFailed  = "failed"
)

// partStatus 单个备份部分的状态（写入 ZIP 内 manifest 与历史索引）。
type partStatus struct {
	State  string `json:"state"`            // ok / skipped / failed
	Size   int64  `json:"size"`             // 该部分字节数
	Reason string `json:"reason,omitempty"` // skipped/failed 时的原因说明
}

// backupParts 四类备份目标的状态集合。
type backupParts struct {
	Database partStatus `json:"database"` // PostgreSQL（pg_dump -Fc）
	Media    partStatus `json:"media"`    // 站点媒体库（data/media）
	Frontend partStatus `json:"frontend"` // 前端源代码 + 构建产物
	Backend  partStatus `json:"backend"`  // 后端源代码 + 二进制
}

// backupItem 单条备份历史（索引条目，也直接作为 API 响应项）。
type backupItem struct {
	File      string      `json:"file"`       // 文件名（backups 目录内）
	Size      int64       `json:"size"`       // 字节数
	CreatedAt string      `json:"created_at"` // 备份时间（RFC3339）
	Parts     backupParts `json:"parts"`      // 各部分状态（v1.3.0 起）
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

// runBackup 执行一次完整备份：按开关采集四类目标 → 单 ZIP 流式落盘 → 写清单
// → 写索引 → 保留清理 → 记状态 → 通知。返回本次备份条目。
func (s *backupStore) runBackup(cfg map[string]string) (*backupItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := backupFilePrefix + time.Now().Format("20060102-150405") + ".zip"
	target := filepath.Join(s.dir, name)
	f, err := os.Create(target)
	if err != nil {
		return nil, err
	}
	bufw := bufio.NewWriter(f)
	zw := zip.NewWriter(bufw)
	parts := s.collectTargets(zw, cfg)
	writeManifest(zw, parts)
	// 逐层关闭（zip 关闭触发条目收尾 → 刷缓冲 → 落盘）；任一失败清理半成品
	zipErr := zw.Close()
	flushErr := bufw.Flush()
	statErr := error(nil)
	var size int64
	if info, err := f.Stat(); err == nil {
		size = info.Size()
	} else {
		statErr = err
	}
	closeErr := f.Close()
	if err := errors.Join(zipErr, flushErr, closeErr, statErr); err != nil {
		_ = os.Remove(target)
		return nil, fmt.Errorf("备份文件写入失败：%w", err)
	}
	if allEnabledFailed(parts, cfg) {
		_ = os.Remove(target)
		return nil, fmt.Errorf("所有启用目标均备份失败（%s）", firstFailure(parts))
	}
	item := &backupItem{File: name, Size: size, CreatedAt: time.Now().Format(time.RFC3339), Parts: parts}
	items := s.loadIndex()
	items = append(items, *item)
	items = pruneBackups(s.dir, items, retentionCount(cfg))
	s.saveIndex(items)
	s.saveState(backupState{LastRun: time.Now()})
	notifyWebhook(cfg["webhook_url"], *item)
	return item, nil
}

// collectTargets 按配置开关依次采集四类备份目标（写入同一 ZIP；互不阻断）。
func (s *backupStore) collectTargets(zw *zip.Writer, cfg map[string]string) backupParts {
	parts := backupParts{}
	// 数据库：pg_dump 流式写入单条目
	if !switchOn(cfg, "backup_db") {
		parts.Database = partStatus{State: stateSkipped, Reason: "未启用"}
	} else if entry, err := zw.Create(entryDatabase); err != nil {
		parts.Database = partStatus{State: stateFailed, Reason: "创建 ZIP 条目失败：" + err.Error()}
	} else if n, err := dumpDatabase(entry, cfg); err != nil {
		parts.Database = partStatus{State: stateFailed, Size: n, Reason: err.Error()}
	} else {
		parts.Database = partStatus{State: stateOK, Size: n}
	}
	// 媒体库：目录树流式打包
	if !switchOn(cfg, "backup_media") {
		parts.Media = partStatus{State: stateSkipped, Reason: "未启用"}
	} else if _, err := os.Stat(mediaDir); err != nil {
		parts.Media = partStatus{State: stateSkipped, Reason: "媒体目录不存在（" + mediaDir + "）"}
	} else if n, err := zipTree(zw, mediaDir, entryMediaPrefix, nil); err != nil {
		parts.Media = partStatus{State: stateFailed, Size: n, Reason: err.Error()}
	} else {
		parts.Media = partStatus{State: stateOK, Size: n}
	}
	// 前端/后端：源代码 + 产物（采集函数内部处理缺失与失败）
	if switchOn(cfg, "backup_frontend") {
		parts = collectFrontend(zw, parts)
	} else {
		parts.Frontend = partStatus{State: stateSkipped, Reason: "未启用"}
	}
	if switchOn(cfg, "backup_backend") {
		parts = collectBackend(zw, parts)
	} else {
		parts.Backend = partStatus{State: stateSkipped, Reason: "未启用"}
	}
	return parts
}

// writeManifest 写入备份内容清单（manifest.json 条目：各部分状态与大小）。
func writeManifest(zw *zip.Writer, parts backupParts) {
	entry, err := zw.Create(entryManifest)
	if err != nil {
		return // 清单写入失败不阻断备份本体
	}
	raw, err := json.MarshalIndent(map[string]any{
		"created_at": time.Now().Format(time.RFC3339),
		"plugin":     pluginID,
		"version":    pluginVersion,
		"parts":      parts,
	}, "", "  ")
	if err != nil {
		return
	}
	_, _ = entry.Write(raw)
}

// allEnabledFailed 判定整体失败：启用中的目标没有任何一个成功（全失败或无可备份）。
func allEnabledFailed(parts backupParts, cfg map[string]string) bool {
	for _, p := range enabledParts(parts, cfg) {
		if p.State == stateOK {
			return false
		}
	}
	return true
}

// enabledParts 取启用中的部分状态列表（纯函数）。
func enabledParts(parts backupParts, cfg map[string]string) []partStatus {
	all := []struct {
		key  string
		part partStatus
	}{
		{"backup_db", parts.Database},
		{"backup_media", parts.Media},
		{"backup_frontend", parts.Frontend},
		{"backup_backend", parts.Backend},
	}
	result := make([]partStatus, 0, len(all))
	for _, item := range all {
		if switchOn(cfg, item.key) {
			result = append(result, item.part)
		}
	}
	return result
}

// firstFailure 取第一个失败部分的描述（整体失败时的错误信息；无 failed 取 skipped 原因）。
func firstFailure(parts backupParts) string {
	all := []partStatus{parts.Database, parts.Media, parts.Frontend, parts.Backend}
	for _, p := range all {
		if p.State == stateFailed && p.Reason != "" {
			return p.Reason
		}
	}
	for _, p := range all {
		if p.Reason != "" {
			return p.Reason
		}
	}
	return "详见备份界面"
}

// switchOn 读开关配置（switch 值为 on/off；未配置按 on 处理——默认全备）。
func switchOn(cfg map[string]string, key string) bool {
	value := strings.TrimSpace(cfg[key])
	return value == "" || value == "on"
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
// 兼容 v1.2.x 旧备份（无 parts 字段——零值 parts 展示为空）。
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
	body, _ := json.Marshal(map[string]any{
		"event":      "backup.done",
		"file":       item.File,
		"size":       item.Size,
		"created_at": item.CreatedAt,
		"parts":      item.Parts,
	})
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "[backup-assistant] 通知发送失败：", err)
		return
	}
	_ = resp.Body.Close()
}
