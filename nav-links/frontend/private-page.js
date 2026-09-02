// nav-links/frontend/private-page.js
// 精品导航 · 私有页直达路由（site.page /plugins/nav-links/private）。
// v1.3.15 起私有导航不占前台导航入口（在精品导航页内切换），本路由仅为
// 已分享/已收藏的直达链接保留——门禁流程复用 private-gate.js（与页内切换同款）。
// ctx: { container, api, user, params: {pluginId, page} }
import { createNavBoard } from "./board.js?v=1";
import { loadPrivateView } from "./private-gate.js?v=1";

export default function registerPage(ctx) {
  // wrapper 承载主题变量作用域（.nl-page 定义 --nl-* 映射）
  const wrapper = document.createElement("div");
  wrapper.className = "nl-page";
  ctx.container.appendChild(wrapper);

  let board = null; // 授权通过后的看板实例
  const gateCleanup = loadPrivateView(wrapper, (data) => {
    board = createNavBoard({ container: wrapper }, { settings: data.settings || {} });
    board.setData(data);
  });

  // 清理函数（销毁看板与门禁、移除容器）。
  return () => {
    gateCleanup();
    if (board) {
      board.destroy();
      board = null;
    }
    wrapper.remove();
  };
}
