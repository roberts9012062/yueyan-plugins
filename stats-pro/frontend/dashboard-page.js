// marketplace-repo/stats-pro/frontend/dashboard-page.js
// 站点统计 · 后台面板页（admin.page /admin/plugin-pages/stats-pro/dashboard）：
//   今日/昨日 PV-UV 卡片 + 累计总量 + 热门内容 Top10（帖内上报的浏览排行）。
// ctx: { container, api, user, params: {pluginId, page} }
import { escapeHtml, cardStyle, hintStyle } from "/plugin-sdk/shared.js";

// metricCard 单指标卡片 DOM 字符串（纯函数）。
function metricCard(label, value) {
  return (
    '<div style="' + cardStyle + ';padding:16px;flex:1;min-width:140px">' +
    '<p style="' + hintStyle + ';margin:0">' + escapeHtml(label) + "</p>" +
    '<p style="margin:6px 0 0;font-size:26px;font-weight:700;color:var(--yy-text,#e8ecf4)">' +
    escapeHtml(String(value)) + "</p></div>"
  );
}

export default function registerPage(ctx) {
  const box = document.createElement("div");
  box.style.cssText = "padding:24px;max-width:760px;margin:0 auto";
  ctx.container.appendChild(box);

  box.innerHTML =
    '<div style="display:flex;align-items:center;gap:12px">' +
    '<span style="display:inline-flex;align-items:center;justify-content:center;width:38px;height:38px;border-radius:50%;background:var(--yy-accent,#5b8cff);color:#fff;font-size:18px;font-weight:700">📈</span>' +
    '<div><h1 style="font-size:18px;font-weight:700;color:var(--yy-text,#e8ecf4);line-height:1.3">站点统计增强</h1>' +
    '<p style="' + hintStyle + '">访客 PV/UV · 热门内容排行（帖内上报）</p></div></div>' +
    '<div style="margin-top:16px;display:flex;gap:12px;flex-wrap:wrap" data-metrics>加载中…</div>' +
    '<div style="margin-top:16px;' + cardStyle + ';padding:16px">' +
    '<p style="font-size:13px;font-weight:600;color:var(--yy-text,#e8ecf4)">热门内容 Top10</p>' +
    '<div style="margin-top:10px" data-top></div>' +
    '</div>' +
    '<p style="margin-top:14px;' + hintStyle + '">提示：前台帖子页浏览时自动上报；管理员访问默认不计数（插件设置可改）。</p>';

  const metricsBox = box.querySelector("[data-metrics]");
  const topBox = box.querySelector("[data-top]");

  // renderTop 热门榜列表（空态引导）。
  const renderTop = (items) => {
    if (!items || items.length === 0) {
      topBox.innerHTML = '<p style="' + hintStyle + '">暂无数据——访客浏览帖子页后此处出现排行。</p>';
      return;
    }
    topBox.innerHTML = "";
    items.forEach((item, idx) => {
      const row = document.createElement("div");
      row.style.cssText = "display:flex;align-items:center;gap:12px;border-top:1px solid var(--yy-border,#2a3348);padding:9px 0";
      const no = document.createElement("span");
      no.style.cssText = "flex-shrink:0;width:22px;height:22px;display:inline-flex;align-items:center;justify-content:center;border-radius:6px;background:var(--yy-bg-2,#1c2436);color:var(--yy-text-3,#8b94a8);font-size:12px";
      no.textContent = String(idx + 1);
      const title = document.createElement("div");
      title.style.cssText = "min-width:0;flex:1;font-size:13px;color:var(--yy-text,#e8ecf4);white-space:nowrap;overflow:hidden;text-overflow:ellipsis";
      const a = document.createElement("a");
      a.href = "/posts/" + encodeURIComponent(item.post_id);
      a.style.cssText = "color:inherit;text-decoration:none";
      a.textContent = "帖子 #" + item.post_id;
      title.appendChild(a);
      const hits = document.createElement("span");
      hits.style.cssText = "flex-shrink:0;font-size:12px;color:var(--yy-text-3,#8b94a8)";
      hits.textContent = item.hits + " 次浏览";
      row.appendChild(no);
      row.appendChild(title);
      row.appendChild(hits);
      topBox.appendChild(row);
    });
  };

  // load 拉取汇总并渲染。
  const load = async () => {
    try {
      const r = await ctx.api.get("/summary");
      if (r.error) {
        metricsBox.textContent = "加载失败：" + r.error;
        return;
      }
      metricsBox.innerHTML =
        metricCard("今日 PV", r.today ? r.today.pv : 0) +
        metricCard("今日 UV", r.today ? r.today.uv : 0) +
        metricCard("昨日 PV", r.yesterday ? r.yesterday.pv : 0) +
        metricCard("累计 PV", r.total_pv) +
        metricCard("累计 UV", r.total_uv);
      renderTop(r.top_posts);
    } catch (e) {
      metricsBox.textContent = "加载失败：" + String(e);
    }
  };

  load();
}
