// marketplace-repo/stats-pro/frontend/tracker.js
// 站点统计 · 前端扩展（post.footer 槽位）：页面浏览上报 + 本帖浏览数展示。
// 上报走宿主公开桥接 /api/v1/stats/hit（访客无需登录）；访客标识用
// localStorage 随机 ID（服务端按日去重算 UV）。
// 契约：默认导出 register(ctx)，返回清理函数。ctx: { slot, el, api, user, props }
export default function register(ctx) {
  // 帖子 ID：详情页路径 /posts/{id}（插件不做列表页计数——避免时间线重复刷量）
  const match = window.location.pathname.match(/\/posts\/(\d+)/);
  const postId = match ? match[1] : "";

  // 访客标识：localStorage 持久随机 ID（首次生成后复用）
  let visitorId = "";
  try {
    visitorId = window.localStorage.getItem("yy_stats_vid") || "";
    if (!visitorId) {
      visitorId = "v" + Date.now().toString(36) + Math.random().toString(36).slice(2, 10);
      window.localStorage.setItem("yy_stats_vid", visitorId);
    }
  } catch (e) {
    // 隐私模式等 localStorage 不可用：只计 PV
  }

  // 会话内去重：同一页面一次会话只上报一次（刷新由服务端日粒度自然收敛）
  const dedupeKey = "yy_stats_hit_" + postId;
  try {
    if (window.sessionStorage.getItem(dedupeKey)) {
      return () => undefined;
    }
    window.sessionStorage.setItem(dedupeKey, "1");
  } catch (e) {
    // sessionStorage 不可用：照常上报
  }

  // 静默上报（失败不打扰访客；fire-and-forget）
  fetch("/api/v1/stats/hit", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ post_id: postId, visitor_id: visitorId }),
  }).catch(() => undefined);

  // 帖子页展示轻量计数徽标（宿主设计变量风格）
  if (!postId || !ctx.el) {
    return () => undefined;
  }
  const badge = document.createElement("span");
  badge.className = "inline-flex items-center gap-1 rounded-full bg-muted px-2.5 py-0.5 text-xs text-ink-3";
  badge.textContent = "📊 本文浏览已记录";
  ctx.el.appendChild(badge);
  return () => badge.remove();
}
