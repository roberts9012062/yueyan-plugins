// nav-links/frontend/nav-page.js
// 精品导航 · 前台公开页（site.page /plugins/nav-links/index，访客无需登录）。
// 只收藏「开放」可见的站点（私有条目在私有页展示）；渲染与交互复用 board.js 通用看板。
// 数据经宿主公开桥接 GET /api/v1/nav/links（匿名可读；不经 ctx.api——其需登录）。
// ctx: { container, api, user, params: {pluginId, page} }
import { createNavBoard } from "./board.js?v=1";

// fetchPublicLinks 拉取公开导航数据（直通插件 JSON）。
async function fetchPublicLinks() {
  const res = await fetch("/api/v1/nav/links", { headers: { Accept: "application/json" } });
  if (!res.ok) {
    throw new Error("HTTP " + res.status);
  }
  return res.json();
}

export default function registerPage(ctx) {
  const board = createNavBoard(ctx, { settings: {} });

  fetchPublicLinks()
    .then((data) => board.setData(data))
    .catch(() => {
      // 拉取失败以空态呈现，并在看板下方追加提示
      board.setData({ links: [], categories: [], tags: [], settings: {} });
      const tip = document.createElement("p");
      tip.style.cssText = "text-align:center;padding:0 0 40px;font-size:13px;color:var(--nl-text2,#9aa6bc)";
      tip.textContent = "导航数据加载失败，请刷新重试";
      ctx.container.querySelector(".nl-page")?.appendChild(tip);
    });

  return () => board.destroy();
}
