// marketplace-repo/backup-assistant/schedule_test.go
// 定点调度计算单测：时刻解析、每日/每周最近应跑时刻、停机补跑判定。
package main

import (
	"testing"
	"time"
)

// TestParseClock 时刻解析：合法/非法输入。
func TestParseClock(t *testing.T) {
	cases := []struct {
		in   string
		want clock
		isOK bool
	}{
		{"03:00", clock{3, 0}, true},
		{"23:59", clock{23, 59}, true},
		{" 8:05 ", clock{8, 5}, true},
		{"24:00", clock{}, false}, // 小时越界
		{"12:60", clock{}, false}, // 分钟越界
		{"0300", clock{}, false},  // 缺分隔符
		{"ab:cd", clock{}, false}, // 非数字
		{"", clock{}, false},
	}
	for _, tc := range cases {
		got, ok := parseClock(tc.in)
		if ok != tc.isOK || got != tc.want {
			t.Errorf("parseClock(%q) = %+v, %v；期望 %+v, %v", tc.in, got, ok, tc.want, tc.isOK)
		}
	}
}

// TestLastDueTimeDaily 每日调度：今日已过取今日、未到取昨日。
func TestLastDueTimeDaily(t *testing.T) {
	c := clock{Hour: 3, Minute: 0}
	after := time.Date(2026, 8, 24, 10, 0, 0, 0, time.Local)
	want := time.Date(2026, 8, 24, 3, 0, 0, 0, time.Local)
	if got := lastDueTime(after, "daily", 0, c); !got.Equal(want) {
		t.Errorf("今日已过：got %v，期望 %v", got, want)
	}
	before := time.Date(2026, 8, 24, 1, 30, 0, 0, time.Local)
	wantYesterday := time.Date(2026, 8, 23, 3, 0, 0, 0, time.Local)
	if got := lastDueTime(before, "daily", 0, c); !got.Equal(wantYesterday) {
		t.Errorf("今日未到：got %v，期望 %v", got, wantYesterday)
	}
}

// TestLastDueTimeWeekly 每周调度：本周已过取本周、未到取上周。
// 2026-08-24 是周一（Weekday=1）；目标周六（day=6）。
func TestLastDueTimeWeekly(t *testing.T) {
	c := clock{Hour: 3, Minute: 0}
	monday := time.Date(2026, 8, 24, 10, 0, 0, 0, time.Local)
	// 本周六尚未到 → 上周六 8-22
	want := time.Date(2026, 8, 22, 3, 0, 0, 0, time.Local)
	if got := lastDueTime(monday, "weekly", 6, c); !got.Equal(want) {
		t.Errorf("本周未到：got %v，期望 %v", got, want)
	}
	// 目标周日（day=0）：周一 10 点时本周日已过（8-23）→ 本周日
	wantSunday := time.Date(2026, 8, 23, 3, 0, 0, 0, time.Local)
	if got := lastDueTime(monday, "weekly", 0, c); !got.Equal(wantSunday) {
		t.Errorf("本周已过：got %v，期望 %v", got, wantSunday)
	}
	// 周一当天定点未到（凌晨 1 点，目标周一 3 点）→ 上周一 8-17
	mondayEarly := time.Date(2026, 8, 24, 1, 0, 0, 0, time.Local)
	wantPrev := time.Date(2026, 8, 17, 3, 0, 0, 0, time.Local)
	if got := lastDueTime(mondayEarly, "weekly", 1, c); !got.Equal(wantPrev) {
		t.Errorf("定点未到：got %v，期望 %v", got, wantPrev)
	}
}

// TestDueForBackup 到点判定：停机补跑（last_run 早于最近应跑时刻即触发）。
func TestDueForBackup(t *testing.T) {
	c := clock{Hour: 3, Minute: 0}
	now := time.Date(2026, 8, 24, 8, 0, 0, 0, time.Local)
	dueToday := time.Date(2026, 8, 24, 3, 0, 0, 0, time.Local)

	// off 不调度
	if dueForBackup(now, dueToday, "off", 0, c) {
		t.Error("schedule=off 不应触发")
	}
	// 今日已备份（last_run 在定点后）：不触发
	if dueForBackup(now, dueToday.Add(time.Hour), "daily", 0, c) {
		t.Error("今日已备份不应重复触发")
	}
	// 昨日备份、今日定点已过：触发（含停机补跑场景）
	if !dueForBackup(now, dueToday.AddDate(0, 0, -1), "daily", 0, c) {
		t.Error("错过今日定点应补跑")
	}
	// 零值 last_run（从未备份）：触发
	if !dueForBackup(now, time.Time{}, "daily", 0, c) {
		t.Error("从未备份应触发")
	}
}
