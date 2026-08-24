// marketplace-repo/backup-assistant/schedule.go
// 定点调度计算：解析 HH:MM 时刻 + 计算「最近一个应跑时刻」（纯函数，便于单测）。
//
// 语义：
//   - daily：每日 schedule_time 执行；今日时刻未到则最近应跑时刻为昨日同时刻；
//   - weekly：每周 weekly_day 的 schedule_time 执行；本周时刻未到则取上周同时刻；
//   - 停机补跑：last_run < 最近应跑时刻 即执行——服务在预定时刻停机时，启动后首个
//     巡检周期自动补跑一次。
package main

import (
	"strconv"
	"strings"
	"time"
)

// clock 时刻（小时 + 分钟）。
type clock struct {
	Hour   int
	Minute int
}

// parseClock 解析 "HH:MM"（容错：允许前导空格；非法返回 false）。
func parseClock(s string) (clock, bool) {
	parts := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(parts) != 2 {
		return clock{}, false
	}
	hour, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return clock{}, false
	}
	minute, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return clock{}, false
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return clock{}, false
	}
	return clock{Hour: hour, Minute: minute}, true
}

// clockOrDefault 解析失败回退 03:00（凌晨低峰，备份默认时刻）。
func clockOrDefault(s string) clock {
	if c, ok := parseClock(s); ok {
		return c
	}
	return clock{Hour: 3, Minute: 0}
}

// weeklyDayOrDefault 解析周几（0=周日 … 6=周六；非法回退 0）。
func weeklyDayOrDefault(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 || n > 6 {
		return 0
	}
	return n
}

// lastDueTime 计算最近的应跑时刻（now 之前、按调度规则最近的定点）。
//   - schedule="daily"：今日 c 时刻；now 尚未到则回退昨日；
//   - schedule="weekly"：本周 day 的 c 时刻；now 尚未到则回退上周；
//   - 其他（off/非法）：返回零值——调用方以零值判定为「不调度」。
func lastDueTime(now time.Time, schedule string, weeklyDay int, c clock) time.Time {
	today := time.Date(now.Year(), now.Month(), now.Day(), c.Hour, c.Minute, 0, 0, now.Location())
	switch schedule {
	case "daily":
		if !now.Before(today) {
			return today
		}
		return today.AddDate(0, 0, -1)
	case "weekly":
		// 距本周目标星期已有天数（Go 的 Weekday()：周日=0 … 周六=6，与配置一致）
		daysBack := (int(now.Weekday()) - weeklyDay + 7) % 7
		due := today.AddDate(0, 0, -daysBack)
		if now.Before(due) {
			return due.AddDate(0, 0, -7)
		}
		return due
	default:
		return time.Time{}
	}
}

// dueForBackup 到点判定：调度开启且上次成功备份早于最近应跑时刻。
// （返回 false 表示无需备份；schedule 为 off/非法值时恒为 false）
func dueForBackup(now time.Time, lastRun time.Time, schedule string, weeklyDay int, c clock) bool {
	due := lastDueTime(now, schedule, weeklyDay, c)
	if due.IsZero() {
		return false
	}
	return lastRun.Before(due)
}
