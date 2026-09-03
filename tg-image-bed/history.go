// marketplace-repo/tg-image-bed/history.go
// 上传历史持久化：插件数据目录 data/plugins/tg-image-bed/history.json。
// 量级定位为单站长图库（千级条目），JSON 文件足够（KISS，不引数据库）；
// file_id 即访问 URL 键，历史同时承担「图库列表」与「删除时反查 message_id」职责。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// historyEntry 单条上传记录。
type historyEntry struct {
	FileID     string `json:"file_id"`     // TG 文件标识（公开，URL 键）
	MessageID  int64  `json:"message_id"`  // 频道消息 ID（deleteMessage 用）
	FileName   string `json:"file_name"`   // 原始文件名
	Size       int64  `json:"size"`        // 字节数（TG 回报）
	Mime       string `json:"mime"`        // MIME 类型
	URL        string `json:"url"`         // 公开访问地址（{proxy_base}/f/{file_id}）
	Mode       string `json:"mode"`        // 发送模式 document/photo
	UploadedAt string `json:"uploaded_at"` // RFC3339
	UploaderID int64  `json:"uploader_id"` // 上传者用户 ID（0=系统/未知）
}

// historyPageSize 列表分页每页条数（与 image-cdn 图库对齐 60）。
const historyPageSize = 60

// historyStore 历史存储（内存切片 + 文件持久化；互斥锁保护并发上传/删除）。
type historyStore struct {
	mu      sync.Mutex
	path    string
	entries []historyEntry // 新在前（追加时插头部）
}

// newHistoryStore 打开/创建历史存储（目录不存在自动创建；文件损坏时从空开始并告警）。
func newHistoryStore(dataDir string) (*historyStore, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	s := &historyStore{path: filepath.Join(dataDir, "history.json")}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil // 首次运行：空历史
		}
		return nil, err
	}
	var entries []historyEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		fmt.Fprintf(os.Stderr, "[tg-image-bed] history.json 损坏，从空历史开始：%v\n", err)
	}
	s.entries = entries
	return s, nil
}

// append 追加一条记录并持久化（补齐时间戳；新在前）。
func (s *historyStore) append(e historyEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.UploadedAt = time.Now().Format(time.RFC3339)
	s.entries = append([]historyEntry{e}, s.entries...)
	return s.persistLocked()
}

// page 分页查询（cursor 为偏移量十进制串，空=第一页；倒序=新在前）。
func (s *historyStore) page(cursor string) ([]historyEntry, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	offset := 0
	if cursor != "" {
		if _, err := fmt.Sscanf(cursor, "%d", &offset); err != nil || offset < 0 {
			offset = 0
		}
	}
	if offset >= len(s.entries) {
		return []historyEntry{}, ""
	}
	end := offset + historyPageSize
	if end > len(s.entries) {
		end = len(s.entries)
	}
	out := make([]historyEntry, end-offset)
	copy(out, s.entries[offset:end])
	next := ""
	if end < len(s.entries) {
		next = fmt.Sprint(end)
	}
	return out, next
}

// find 按 file_id 查单条（不存在返回 nil；副本返回防外部改内部状态）。
func (s *historyStore) find(fileID string) *historyEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.entries {
		if s.entries[i].FileID == fileID {
			e := s.entries[i]
			return &e
		}
	}
	return nil
}

// remove 按 file_id 批量移除记录（返回移除数；TG 消息删除由调用方先行处理）。
func (s *historyStore) remove(fileIDs []string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	drop := make(map[string]bool, len(fileIDs))
	for _, id := range fileIDs {
		drop[id] = true
	}
	kept := make([]historyEntry, 0, len(s.entries))
	removed := 0
	for _, e := range s.entries {
		if drop[e.FileID] {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	if removed > 0 {
		s.entries = kept
		_ = s.persistLocked()
	}
	return removed
}

// persistLocked 落盘（调用方须持锁；临时文件 + rename 原子替换，防写一半损坏）。
func (s *historyStore) persistLocked() error {
	raw, err := json.MarshalIndent(s.entries, "", " ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
