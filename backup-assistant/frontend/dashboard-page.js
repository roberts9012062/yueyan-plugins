// marketplace-repo/backup-assistant/frontend/dashboard-page.js
// 备份助手 · 后台仪表页（admin.page /admin/plugin-pages/backup-assistant/dashboard）：
//   调度状态卡（周期/保留/上次备份）+ 立即备份按钮 + 备份历史列表。
// ctx: { container, api, user, params: {pluginId, page} }
// 样式常量复用宿主共享 SDK（/plugin-sdk/shared.js，同源 ESM）。
import { escapeHtml, cardStyle, hintStyle } from "/plugin-sdk/shared.js";

// SCHEDULE_LABEL 调度周期文案（与插件设置项 select 选项对应）。
const SCHEDULE_LABEL = { off: "已关闭", daily: "每天一次", weekly: "每周一次" };

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
  const box = document.createElement("div");
  box.style.cssText = "padding:24px;max-width:760px;margin:0 auto";
  ctx.container.appendChild(box);

  box.innerHTML =
    '<div style="display:flex;align-items:center;gap:12px">' +
    '<span style="display:inline-flex;align-items:center;justify-content:center;width:38px;height:38px;border-radius:50%;background:var(--yy-accent,#5b8cff);color:#fff;font-size:18px;font-weight:700">⭳</span>' +
    '<div><h1 style="font-size:18px;font-weight:700;color:var(--yy-text,#e8ecf4);line-height:1.3">备份助手</h1>' +
    '<p style="' + hintStyle + '">媒体库 ZIP 备份 · 保留策略自动清理 · webhook 通知</p></div></div>' +
    '<div style="margin-top:16px;' + cardStyle + ';padding:16px" data-status-card>' +
    '<div style="display:flex;align-items:center;justify-content-between;gap:12px;flex-wrap:wrap">' +
    '<div data-status-text style="' + hintStyle + '">加载中…</div>' +
    '<button type="button" data-run-btn style="height:38px;border-radius:8px;padding:0 18px;font-size:13px;color:#fff;background:var(--yy-accent,#5b8cff);border:none;cursor:pointer">立即备份</button>' +
    '</div></div>' +
    '<div style="margin-top:16px;' + cardStyle + ';padding:16px">' +
    '<p style="font-size:13px;font-weight:600;color:var(--yy-text,#e8ecf4)">备份历史</p>' +
    '<div style="margin-top:10px" data-history></div>' +
    '</div>' +
    '<p style="margin-top:14px;' + hintStyle + '">提示：定时周期、保留份数与通知地址在「我的插件 → 备份助手 → 设置」中配置。</p>';

  const statusText = box.querySelector("[data-status-text]");
  const runBtn = box.querySelector("[data-run-btn]");
  const historyBox = box.querySelector("[data-history]");

  // renderHistory 渲染历史列表（空态给引导文案）。
  const renderHistory = (items) => {
    if (!items || items.length === 0) {
      historyBox.innerHTML = '<p style="' + hintStyle + '">还没有备份记录，点击上方「立即备份」创建第一份。</p>';
      return;
    }
    historyBox.innerHTML = "";
    for (const item of items) {
      const row = document.createElement("div");
      row.style.cssText = "display:flex;align-items:center;gap:12px;border-top:1px solid var(--yy-border,#2a3348);padding:10px 0";
      const name = document.createElement("div");
      name.style.cssText = "min-width:0;flex:1";
      name.innerHTML =
        '<p style="font-size:13px;color:var(--yy-text,#e8ecf4);white-space:nowrap;overflow:hidden;text-overflow:ellipsis">' +
        escapeHtml(item.file) + "</p>" +
        '<p style="' + hintStyle + '">' + escapeHtml(formatTime(item.created_at)) + "</p>";
      const size = document.createElement("span");
      size.style.cssText = "flex-shrink:0;font-size:12px;color:var(--yy-text-3,#8b94a8)";
      size.textContent = formatSize(item.size);
      row.appendChild(name);
      row.appendChild(size);
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
      statusText.textContent =
        "定时备份：" + schedule + " · 保留 " + (r.retention ?? "-") + " 份 · 上次备份：" + (r.last_run || "尚未备份");
      renderHistory(r.items);
    } catch (e) {
      statusText.textContent = "加载失败：" + String(e);
    }
  };

  // 立即备份（按钮期间禁用防连点）。
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
