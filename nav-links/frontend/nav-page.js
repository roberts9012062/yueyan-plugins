// nav-links/frontend/nav-page.js
// 精品导航 · 前台公开页（site.page /plugins/nav-links/index，访客无需登录）。
// 页内「公开 / 私有」切换：公开看板展示开放站点；私有走门禁流程（self=站长登录态 /
// password=访问密码解锁）后展示私有站点——私有导航不单独占据前台导航入口。
// 看板渲染复用 board.js，门禁流程复用 private-gate.js。
// ctx: { container, api, user, params: {pluginId, page} }
import { createNavBoard } from "./board.js?v=1";
import { loadPrivateView } from "./private-gate.js?v=1";

// fetchPublicLinks 拉取公开导航数据（直通插件 JSON；仅开放条目）。
async function fetchPublicLinks() {
  const res = await fetch("/api/v1/nav/links", { headers: { Accept: "application/json" } });
  if (!res.ok) {
    throw new Error("HTTP " + res.status);
  }
  return res.json();
}

export default function registerPage(ctx) {
  // wrapper 承载主题变量作用域（.nl-page 定义 --nl-* 映射）与切换控件/看板
  const wrapper = document.createElement("div");
  wrapper.className = "nl-page";
  ctx.container.appendChild(wrapper);

  let board = null; // 当前看板实例（公开/私有共用一个容器，切换时销毁重建）
  let gateCleanup = null; // 门禁流程清理句柄（异步回调防悬挂）
  let tab = "public"; // 当前页签：public | private

  // ---------- 骨架：页签胶囊 + 内容区 ----------
  wrapper.innerHTML =
    '<div style="display:flex;justify-content:center;margin-top:16px">' +
    '<span style="display:inline-flex;border:1px solid var(--nl-border,#2a3348);border-radius:999px;overflow:hidden">' +
    '<button type="button" data-tab="public" title="所有人可见的收藏站点" style="height:34px;padding:0 18px;border:none;background:transparent;font-size:13px;cursor:pointer">🌐 公开导航</button>' +
    '<button type="button" data-tab="private" title="仅自己可见或凭密码访问的收藏站点" style="height:34px;padding:0 18px;border:none;background:transparent;font-size:13px;cursor:pointer">🔒 私有导航</button>' +
    "</span></div>" +
    '<div data-body></div>';
  const bodyEl = wrapper.querySelector("[data-body]");

  // renderTabs 高亮当前页签（选中=实底强调）。
  const renderTabs = () => {
    wrapper.querySelectorAll("[data-tab]").forEach((btn) => {
      const on = btn.dataset.tab === tab;
      btn.style.background = on ? "var(--nl-accent,#a8b8d8)" : "transparent";
      btn.style.color = on ? "var(--nl-on-accent,#0b0f1a)" : "var(--nl-text2,#9aa6bc)";
      btn.style.fontWeight = on ? "600" : "400";
    });
  };

  // clearView 销毁当前视图（看板或门禁），清空内容区。
  const clearView = () => {
    if (board) {
      board.destroy();
      board = null;
    }
    if (gateCleanup) {
      gateCleanup();
      gateCleanup = null;
    }
    bodyEl.innerHTML = "";
  };

  // mountBoard 在内容区挂看板并注入数据（公开/私有共用）。
  const mountBoard = (data) => {
    board = createNavBoard({ container: bodyEl }, { settings: data.settings || {} });
    board.setData(data);
  };

  // showPublic 公开视图：拉开放条目直出看板。
  const showPublic = async () => {
    clearView();
    bodyEl.innerHTML = '<p style="text-align:center;padding:50px 0;font-size:13px;color:var(--nl-text2,#9aa6bc)">正在加载…</p>';
    try {
      const data = await fetchPublicLinks();
      if (tab !== "public") {
        return; // 期间已切走，丢弃
      }
      clearView();
      mountBoard(data);
    } catch (e) {
      if (tab === "public") {
        clearView();
        mountBoard({ links: [], categories: [], tags: [], settings: {} });
        const tip = document.createElement("p");
        tip.style.cssText = "text-align:center;padding:0 0 40px;font-size:13px;color:var(--nl-text2,#9aa6bc)";
        tip.textContent = "导航数据加载失败，请刷新重试";
        bodyEl.appendChild(tip);
      }
    }
  };

  // showPrivate 私有视图：门禁流程（管理员/token 直进；密码解锁）通过后挂看板。
  const showPrivate = () => {
    clearView();
    gateCleanup = loadPrivateView(bodyEl, (data) => {
      mountBoard(data);
    });
  };

  // ---------- 页签事件 ----------
  wrapper.querySelectorAll("[data-tab]").forEach((btn) =>
    btn.addEventListener("click", () => {
      if (tab === btn.dataset.tab) {
        return;
      }
      tab = btn.dataset.tab;
      renderTabs();
      if (tab === "private") {
        showPrivate();
      } else {
        showPublic();
      }
    })
  );

  renderTabs();
  showPublic();

  // 清理函数（销毁看板与门禁、移除容器）。
  return () => {
    clearView();
    wrapper.remove();
  };
}
