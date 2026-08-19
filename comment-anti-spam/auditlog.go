// marketplace-repo/comment-anti-spam/auditlog.go
// 审计日志：命中记录追加写入插件数据目录 logs/audit.log（时间/规则/明细/内容摘要）。
// 文件行数上限滚动（超过上限截掉最旧一半），避免长年运行无限膨胀。
package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 审计日志参数（滚动阈值与内容摘要长度）。
const (
	auditMaxLines      = 2000              // 文件行数上限（超出截掉最旧一半）
	auditContentLimit  = 120               // 留痕内容摘要长度（rune）
	auditFlushInterval = 3 * time.Second   // 落盘间隔（批量 flush 减少 IO）
)

// auditLog 审计日志句柄（并发安全；record 异步刷盘，close 前同步落盘）。
type auditLog struct {
	mu     sync.Mutex
	path   string
	buf    []string // 待写缓冲
	closed bool
}

// newAuditLog 创建审计日志（目录：data/plugins/{id}/logs/audit.log，相对宿主工作目录）。
func newAuditLog(pluginID string) (*auditLog, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(wd, "data", "plugins", pluginID, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	log := &auditLog{path: filepath.Join(dir, "audit.log")}
	go log.flushLoop()
	return log, nil
}

// flushLoop 定时落盘循环（间隔批量写；句柄关闭后由 close 兜底落盘）。
func (l *auditLog) flushLoop() {
	ticker := time.NewTicker(auditFlushInterval)
	defer ticker.Stop()
	for range ticker.C {
		if !l.flush() {
			return // 已关闭：退出循环
		}
	}
}

// record 记录一条命中（时间 / 规则 / 明细 / 内容摘要）。
func (l *auditLog) record(rule string, detail string, content string) {
	summary := []rune(strings.ReplaceAll(content, "\n", " "))
	if len(summary) > auditContentLimit {
		summary = summary[:auditContentLimit]
	}
	line := time.Now().Format("2006-01-02 15:04:05") + "\t" + rule + "\t" + detail + "\t" + string(summary)
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed {
		l.buf = append(l.buf, line)
	}
}

// flush 落盘缓冲（返回 false=句柄已关闭；超行滚动截断最旧一半）。
func (l *auditLog) flush() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return false
	}
	if len(l.buf) == 0 {
		return true
	}
	if err := appendLines(l.path, l.buf); err != nil {
		return true // 写失败丢弃本批（审计日志尽力而为，不反噬主流程）
	}
	l.buf = l.buf[:0]
	rotateIfNeeded(l.path)
	return true
}

// close 关闭句柄（同步落盘剩余缓冲）。
func (l *auditLog) close() error {
	l.mu.Lock()
	l.closed = true
	pending := l.buf
	l.buf = nil
	l.mu.Unlock()
	if len(pending) == 0 {
		return nil
	}
	return appendLines(l.path, pending)
}

// appendLines 追加多行到文件（不存在则创建）。
func appendLines(path string, lines []string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, line := range lines {
		if _, err := f.WriteString(line + "\n"); err != nil {
			return err
		}
	}
	return nil
}

// rotateIfNeeded 超行滚动：读取全文件，超过上限则仅保留最新一半（原地重写）。
func rotateIfNeeded(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) <= auditMaxLines {
		return
	}
	keep := lines[len(lines)-auditMaxLines/2:]
	_ = os.WriteFile(path, []byte(strings.Join(keep, "\n")+"\n"), 0o644)
}
