// marketplace-repo/backup-assistant/frontend/dashboard-page.js
// 备份助手 · 后台仪表页（admin.page /admin/plugin-pages/backup-assistant/dashboard）：
//   调度状态卡（定点时刻/内容开关/保留/上次备份）+ 立即备份按钮 + 备份历史
//   （各部分状态徽标 + 下载）。
// ctx: { container, api, user, params: {pluginId, page} }
// 样式常量复用宿主共享 SDK（/plugin-sdk/shared.js，同源 ESM）。
import { escapeHtml, cardStyle, hintStyle } from "/plugin-sdk/shared.js";

// SCHEDULE_LABEL 调度周期文案（与插件设置项 select 选项对应）。
const SCHEDULE_LABEL = { off: "已关闭", daily: "每天一次", weekly: "每周一次" };

// PART_META 备份部分元信息（键与 /history parts 字段对应）。
const PART_META = [
  { key: "database", label: "数据库" },
  { key: "media", label: "媒体" },
  { key: "frontend", label: "前端" },
  { key: "backend", label: "后端" },
];

// PART_COLORS 部分状态徽标配色（ok=绿、skipped=灰、failed=红）。
const PART_COLORS = {
  ok: "rgba(74,222,128,.16);color:#4ade80",
  skipped: "rgba(148,163,184,.14);color:#94a3b8",
  failed: "rgba(248,113,113,.16);color:#f87171",
};

// badgeHtml 生成状态徽标 HTML（title 悬浮显示原因/大小）。
function badgeHtml(label, part) {
  const color = PART_COLORS[part.state] || PART_COLORS.skipped;
  const title = part.state === "ok"
    ? label + " " + formatSize(part.size)
    : label + "：" + (part.reason || part.state);
  return '<span title="' + escapeHtml(title) + '" style="flex-shrink:0;font-size:11px;line-height:1;padding:4px 8px;border-radius:999px;background:' + color + '">' +
    (part.state === "ok" ? "✓ " : part.state === "failed" ? "✕ " : "○ ") + escapeHtml(label) + "</span>";
}

// formatSize 字节数人性化（KB/MB/GB）。
function formatSize(bytes) {
  if (!bytes || bytes <= 0) return "0 B";
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + " KB";
  if (bytes < 1024 * 1024 * 1024) return (bytes / 1024 / 1024).toFixed(1) + " MB";
  return (bytes / 1024 / 1024 / 1024).toFixed(2) + " GB";
}

// formatTime RFC3339 → 本地可读时间。
function formatTime(iso) {
  if (!iso) return "—";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  return d.toLocaleString("zh-CN", { hour12: false });
}

export default function registerPage(ctx) {
  const pluginId = ctx.params?.pluginId || "backup-assistant";
  const box = document.createElement("div");
  box.style.cssText = "padding:24px;max-width:760px;margin:0 auto";
  ctx.container.appendChild(box);

  box.innerHTML =
    '<div style="display:flex;align-items:center;gap:12px">' +
    '<span style="display:inline-flex;align-items:center;justify-content:center;width:38px;height:38px;border-radius:50%;background:var(--yy-accent,#5b8cff);color:#fff;font-size:18px;font-weight:700">⭳</span>' +
    '<div><h1 style="font-size:18px;font-weight:700;color:var(--yy-text,#e8ecf4);line-height:1.3">备份助手</h1>' +
    '<p style="' + hintStyle + '">数据库 + 媒体库 + 前端/后端源代码与产物 · 定点定时 · 保留清理</p></div></div>' +
    '<div style="margin-top:16px;' + cardStyle + ';padding:16px" data-status-card>' +
    '<div style="display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap">' +
    '<div data-status-text style="' + hintStyle + '">加载中…</div>' +
    '<button type="button" data-run-btn style="height:38px;border-radius:8px;padding:0 18px;font-size:13px;color:#fff;background:var(--yy-accent,#5b8cff);border:none;cursor:pointer">立即备份</button>' +
    '</div><div data-switch-row style="display:flex;gap:8px;flex-wrap:wrap;margin-top:12px"></div></div>' +
    '<div style="margin-top:16px;' + cardStyle + ';padding:16px">' +
    '<p style="font-size:13px;font-weight:600;color:var(--yy-text,#e8ecf4)">备份历史</p>' +
    '<div style="margin-top:10px" data-history></div>' +
    '</div>' +
    '<p style="margin-top:14px;' + hintStyle + '">提示：定点时刻、内容开关、pg_dump 路径与通知地址在「我的插件 → 备份助手 → 设置」中配置。</p>';

  const statusText = box.querySelector("[data-status-text]");
  const switchRow = box.querySelector("[data-switch-row]");
  const runBtn = box.querySelector("[data-run-btn]");
  const historyBox = box.querySelector("[data-history]");

  // renderSwitches 渲染当前内容开关徽标行（全关时提示）。
  const renderSwitches = (r) => {
    const switches = [
      { label: "数据库", on: r.backup_db },
      { label: "媒体库", on: r.backup_media },
      { label: "前端", on: r.backup_frontend },
      { label: "后端", on: r.backup_backend },
    ];
    switchRow.innerHTML = switches.map((s) =>
      '<span style="flex-shrink:0;font-size:11px;line-height:1;padding:4px 8px;border-radius:999px;background:' +
      (s.on ? "rgba(91,140,255,.16);color:#8fb0ff" : "rgba(148,163,184,.14);color:#94a3b8") + '">' +
      (s.on ? "● " : "○ ") + s.label + "</span>").join("");
  };

  // renderHistory 渲染历史列表（各部分状态徽标 + 大小 + 下载按钮）。
  const renderHistory = (items) => {
    if (!items || items.length === 0) {
      historyBox.innerHTML = '<p style="' + hintStyle + '">还没有备份记录，点击上方「立即备份」创建第一份。</p>';
      return;
    }
    historyBox.innerHTML = "";
    for (const item of items) {
      const row = document.createElement("div");
      row.style.cssText = "border-top:1px solid var(--yy-border,#2a3348);padding:10px 0";
      const name = document.createElement("div");
      name.style.cssText = "min-width:0;flex:1";
      name.innerHTML =
        '<p style="font-size:13px;color:var(--yy-text,#e8ecf4);white-space:nowrap;overflow:hidden;text-overflow:ellipsis">' +
        escapeHtml(item.file) + "</p>" +
        '<p style="' + hintStyle + '">' + escapeHtml(formatTime(item.created_at)) + " · " + formatSize(item.size) + "</p>";
      if (item.parts) {
        const badges = document.createElement("div");
        badges.style.cssText = "display:flex;gap:6px;flex-wrap:wrap;margin-top:6px";
        badges.innerHTML = PART_META
          .filter((m) => item.parts[m.key])
          .map((m) => badgeHtml(m.label, item.parts[m.key]))
          .join("");
        name.appendChild(badges);
      }
      const actions = document.createElement("div");
      actions.style.cssText = "flex-shrink:0";
      const dlBtn = document.createElement("button");
      dlBtn.type = "button";
      dlBtn.textContent = "下载";
      dlBtn.style.cssText = "height:30px;border-radius:6px;padding:0 12px;font-size:12px;color:#fff;background:var(--yy-bg-2,#202a40);border:1px solid var(--yy-border,#2a3348);cursor:pointer";
      // 下载走宿主流式端点（带主站凭证；ctx.api.download 由宿主 loader 提供）
      dlBtn.addEventListener("click", () => {
        dlBtn.disabled = true;
        const restore = () => { dlBtn.disabled = false; };
        if (typeof ctx.api.download !== "function") {
          statusText.textContent = "下载失败：宿主前端暂不支持下载方法，请升级宿主";
          restore();
          return;
        }
        ctx.api
          .download("/api/v1/admin/plugins/" + encodeURIComponent(pluginId) + "/backups/" + encodeURIComponent(item.file) + "/download", item.file)
          .then(restore)
          .catch((e) => { statusText.textContent = "下载失败：" + String(e); restore(); });
      });
      actions.appendChild(dlBtn);
      row.style.cssText += ";display:flex;align-items:center;gap:12px";
      row.appendChild(name);
      row.appendChild(actions);
      historyBox.appendChild(row);
    }
  };

  // load 刷新状态与历史。
  const load = async () => {
    try {
      const r = await ctx.api.get("/history");
      if (r.error) {
        statusText.textContent = "加载失败：" + r.error;
        return;
      }
      const schedule = SCHEDULE_LABEL[r.schedule] || r.schedule || "未知";
      const when = r.schedule === "weekly"
        ? (r.weekly_label || "周日") + " " + (r.schedule_time || "03:00")
        : (r.schedule_time || "03:00");
      statusText.textContent = r.schedule === "off"
        ? "定时备份：已关闭 · 保留 " + (r.retention ?? "-") + " 份 · 上次备份：" + (r.last_run || "尚未备份")
        : "定时备份：" + schedule + " " + when + " · 保留 " + (r.retention ?? "-") + " 份 · 上次备份：" + (r.last_run || "尚未备份");
      renderSwitches(r);
      renderHistory(r.items);
    } catch (e) {
      statusText.textContent = "加载失败：" + String(e);
    }
  };

  // 立即备份（按钮期间禁用防连点；备份含全量目录可能耗时较长）。
  runBtn.addEventListener("click", async () => {
    runBtn.disabled = true;
    const prev = runBtn.textContent;
    runBtn.textContent = "备份中…";
    try {
      const r = await ctx.api.post("/run");
      if (r.error) {
        statusText.textContent = "备份失败：" + r.error;
      } else {
        statusText.textContent = "✓ 备份完成：" + r.file + "（" + formatSize(r.size) + "）";
        await load();
      }
    } catch (e) {
      statusText.textContent = "备份失败：" + String(e);
    } finally {
      runBtn.disabled = false;
      runBtn.textContent = prev;
    }
  });

  load();
}
