// nav-links/frontend/nav-page.js
// 精品导航 · 前台公开页（site.page /plugins/nav-links/index，访客无需登录）。
// 布局：顶部标题与搜索 → 三栏（左分类菜单 / 中导航卡片网格 / 右 3D 球形标签云）。
// 中栏支持双视图：列表式（默认，图标左内容右）与九宫格（图标居中紧凑方格），
// 选择记忆在 localStorage（同源，随浏览器持久）。
// 数据经宿主公开桥接 GET /api/v1/nav/links（匿名可读；不经 ctx.api——其需登录）。
// ctx: { container, api, user, params: {pluginId, page} }
import { escapeHtml } from "/plugin-sdk/shared.js";
import { createTagSphere } from "./tag-sphere.js?v=4";

// hostHue 由域名稳定映射一个色相（无图标站点的首字母色块着色；纯函数）。
function hostHue(host) {
  let h = 0;
  for (const ch of host) {
    h = (h * 31 + ch.codePointAt(0)) % 360;
  }
  return h;
}

// fetchPublicLinks 拉取公开导航数据（直通插件 JSON）。
async function fetchPublicLinks() {
  const res = await fetch("/api/v1/nav/links", { headers: { Accept: "application/json" } });
  if (!res.ok) {
    throw new Error("HTTP " + res.status);
  }
  return res.json();
}

// viewStorageKey 视图偏好存储键（list=列表式 / grid=九宫格）。
const viewStorageKey = "nav-links-view";

// readStoredView 读取视图偏好（非法值回退列表式；纯函数）。
function readStoredView() {
  const v = localStorage.getItem(viewStorageKey);
  return v === "grid" ? "grid" : "list";
}

// pageStyles 页面级样式（含响应式断点；随页面卸载移除）。
const pageStyles = `
.nl-page{--nl-border:var(--yy-border,#2a3348);--nl-elev:var(--yy-elev,#fff);--nl-text:var(--yy-text,#e8ecf4);--nl-text2:var(--yy-text-2,#9aa6bc);--nl-accent:var(--yy-accent,#6366f1);--nl-soft:var(--yy-accent-soft,#6366f120);--nl-muted:var(--yy-muted,#161c2b)}
.nl-layout{display:flex;gap:16px;align-items:flex-start;margin-top:14px}
.nl-aside{flex:none;width:170px;border:1px solid var(--nl-border);border-radius:14px;background:var(--nl-elev);padding:12px;position:sticky;top:16px}
.nl-aside h3{margin:2px 4px 8px;font-size:12px;font-weight:600;color:var(--nl-text2);letter-spacing:.5px}
.nl-menu{display:flex;flex-direction:column;gap:2px}
.nl-menu button{display:flex;align-items:center;justify-content:space-between;gap:6px;height:34px;padding:0 10px;border:none;border-left:3px solid transparent;border-radius:8px;background:transparent;font-size:13px;color:var(--nl-text2);cursor:pointer;text-align:left;transition:background .15s,color .15s}
.nl-menu button:hover{background:var(--nl-soft);color:var(--nl-text)}
.nl-menu button.nl-on{background:var(--nl-soft);border-left-color:var(--nl-accent);color:var(--nl-accent);font-weight:600}
.nl-menu .nl-n{font-size:11px;opacity:.75;flex:none}
.nl-main{flex:1;min-width:0}
.nl-status{display:flex;align-items:center;gap:8px;margin-bottom:10px;font-size:12px;color:var(--nl-text2);min-height:24px;flex-wrap:wrap}
.nl-status .nl-chip{display:inline-flex;align-items:center;gap:4px;padding:2px 10px;border-radius:999px;background:var(--nl-soft);color:var(--nl-accent);font-size:12px}
.nl-status .nl-clear{border:none;background:transparent;color:var(--nl-text2);cursor:pointer;font-size:12px;text-decoration:underline;padding:0}
.nl-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(250px,1fr));gap:10px}
.nl-grid[data-view="grid"]{grid-template-columns:repeat(auto-fill,minmax(132px,1fr));gap:10px}
.nl-card{display:flex;gap:12px;padding:14px;border-radius:14px;border:1px solid var(--nl-border);background:var(--nl-elev);text-decoration:none;transition:border-color .15s,transform .15s}
.nl-card:hover{border-color:var(--nl-accent);transform:translateY(-2px)}
.nl-view{display:inline-flex;gap:2px;margin-left:auto;border:1px solid var(--nl-border);border-radius:8px;overflow:hidden;flex:none}
.nl-view button{height:24px;width:30px;border:none;background:transparent;color:var(--nl-text2);font-size:13px;cursor:pointer;line-height:1}
.nl-view button:hover{color:var(--nl-text)}
.nl-view button.nl-on{background:var(--nl-soft);color:var(--nl-accent)}
.nl-card-grid{flex-direction:column;align-items:center;text-align:center;gap:8px;padding:16px 10px 12px}
.nl-card-grid .nl-g-name{max-width:100%;font-size:13px;font-weight:600;color:var(--nl-text);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.nl-card-grid .nl-g-cat{font-size:10px;padding:1px 8px;border-radius:999px;background:var(--nl-soft);color:var(--nl-accent);max-width:100%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.nl-sphere-wrap{flex:none;width:250px;border:1px solid var(--nl-border);border-radius:14px;background:var(--nl-elev);padding:12px;position:sticky;top:16px}
.nav-sphere{position:relative;height:280px;cursor:grab;overflow:hidden;touch-action:none}
.nav-sphere:active{cursor:grabbing}
.nav-sphere-tag{position:absolute;left:0;top:0;display:inline-flex;align-items:center;gap:4px;height:24px;padding:0 10px;border-radius:999px;border:1px solid var(--nl-border);background:var(--nl-elev);font-size:12px;color:var(--nl-text2);cursor:pointer;white-space:nowrap;will-change:transform,opacity;user-select:none}
.nav-sphere-tag:hover{color:var(--nl-text);border-color:var(--nl-accent)}
.nav-sphere-tag-active{background:var(--nl-accent);border-color:var(--nl-accent);color:#fff;font-weight:600}
.nav-sphere-count{font-size:10px;opacity:.65}
@media (max-width:900px){
 .nl-layout{flex-direction:column}
 .nl-aside,.nl-sphere-wrap{width:100%;position:static}
 .nl-menu{flex-direction:row;flex-wrap:wrap}
 .nav-sphere{height:220px}
}`;

export default function registerPage(ctx) {
  const state = { links: [], categories: [], tags: [], settings: {}, keyword: "", filterCat: "", filterTag: "", loaded: false, view: readStoredView() };
  let sphere = null; // 3D 标签云实例（重渲染时销毁重建）

  const styleEl = document.createElement("style");
  styleEl.textContent = pageStyles;
  document.head.appendChild(styleEl);

  const box = document.createElement("div");
  box.className = "nl-page";
  ctx.container.appendChild(box);

  // ---------- 过滤（纯函数） ----------
  const visibleLinks = () =>
    state.links.filter((l) => {
      if (state.filterCat && (l.category || "未分类") !== state.filterCat) return false;
      if (state.filterTag && !(l.tags || []).includes(state.filterTag)) return false;
      if (state.keyword) {
        const kw = state.keyword.toLowerCase();
        const hay = (l.name + " " + l.url + " " + (l.description || "") + " " + (l.tags || []).join(" ")).toLowerCase();
        if (!hay.includes(kw)) return false;
      }
      return true;
    });

  const catCount = (name) => state.links.filter((l) => (l.category || "未分类") === name).length;
  const tagCount = (name) => state.links.filter((l) => (l.tags || []).includes(name)).length;

  // menuCategories 分类菜单数据：聚合分类 + 兜底"未分类"（存在无分类条目时）。
  const menuCategories = () => {
    const cats = state.categories.slice();
    if (state.links.some((l) => !l.category)) {
      cats.push("未分类");
    }
    return cats;
  };

  // hostOf 提取域名（失败回退原始地址；纯函数）。
  const hostOf = (l) => {
    try {
      return new URL(l.url).hostname;
    } catch (e) {
      return l.url;
    }
  };

  // iconHTML 图标（有图标用 img，否则首字母色块；九宫格与列表共用，尺寸外部控制；纯函数）。
  const iconHTML = (l, size) => {
    if (l.icon) {
      return '<img src="' + escapeHtml(l.icon) + '" alt="" style="width:' + size + 'px;height:' + size + 'px;border-radius:10px;object-fit:contain;flex:none">';
    }
    const host = hostOf(l);
    return (
      '<span style="width:' + size + "px;height:" + size + 'px;border-radius:10px;flex:none;display:inline-flex;align-items:center;justify-content:center;font-size:' + Math.round(size * 0.4) + 'px;font-weight:700;color:#fff;background:hsl(' + hostHue(host) + ',55%,45%)">' + escapeHtml((l.name || host).charAt(0)) + "</span>"
    );
  };

  // cardAttr 链接公共属性（地址 + 新窗口开关；class 由各视图卡片自带；纯函数）。
  const cardAttr = (l) =>
    'href="' + escapeHtml(l.url) + '" target="' + (state.settings.open_new_tab !== "off" ? "_blank" : "_self") + '" rel="noopener noreferrer"';

  // rowCardHTML 列表式卡片（默认：图标左、内容右）。
  const rowCardHTML = (l) => {
    const tip = l.name + (l.description ? " · " + l.description : "");
    return (
      '<a ' + cardAttr(l) + ' class="nl-card" title="' + escapeHtml(tip) + '">' +
      iconHTML(l, 44) +
      '<div style="flex:1;min-width:0">' +
      '<div style="display:flex;align-items:center;gap:8px">' +
      '<span style="font-size:14px;font-weight:600;color:var(--nl-text);overflow:hidden;text-overflow:ellipsis;white-space:nowrap">' + escapeHtml(l.name) + "</span>" +
      '<span style="font-size:11px;padding:1px 8px;border-radius:999px;flex:none;background:var(--nl-soft);color:var(--nl-accent)">' + escapeHtml(l.category || "未分类") + "</span></div>" +
      (l.description ? '<p style="margin:4px 0 0;font-size:12px;color:var(--nl-text2);line-height:1.5;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden">' + escapeHtml(l.description) + "</p>" : "") +
      (l.tags && l.tags.length ? '<div style="margin-top:6px;display:flex;gap:4px;flex-wrap:wrap">' + l.tags.map((t) => '<span style="font-size:11px;padding:1px 8px;border-radius:999px;border:1px solid var(--nl-border);color:var(--nl-text2)">' + escapeHtml(t) + "</span>").join("") + "</div>" : "") +
      "</div></a>"
    );
  };

  // gridCardHTML 九宫格卡片（图标居中：名称 + 分类，简介/标签进 title 悬停提示）。
  const gridCardHTML = (l) => {
    const tip = l.name + (l.description ? " · " + l.description : "") + ((l.tags || []).length ? " · " + l.tags.join(" / ") : "");
    return (
      '<a ' + cardAttr(l) + ' class="nl-card nl-card-grid" title="' + escapeHtml(tip) + '">' +
      iconHTML(l, 52) +
      '<span class="nl-g-name">' + escapeHtml(l.name) + "</span>" +
      '<span class="nl-g-cat">' + escapeHtml(l.category || "未分类") + "</span></a>"
    );
  };

  const render = () => {
    const s = state.settings;
    const rows = visibleLinks();
    const cats = menuCategories();

    box.innerHTML =
      '<div style="text-align:center;padding:10px 0 6px">' +
      '<h1 style="font-size:26px;font-weight:800;color:var(--nl-text);letter-spacing:.5px">' + escapeHtml(s.page_title || "精品导航") + "</h1>" +
      '<p style="margin:6px 0 0;font-size:13px;color:var(--nl-text2)">' + escapeHtml(s.page_subtitle || "收藏的优质站点") + "</p></div>" +
      '<div style="max-width:420px;margin:16px auto 0">' +
      '<input data-kw type="text" placeholder="搜索站点…" style="height:40px;width:100%;border-radius:999px;border:1px solid var(--nl-border);background:var(--nl-elev);color:var(--nl-text);padding:0 18px;font-size:13px;outline:none;box-sizing:border-box;text-align:center"></div>' +
      '<div class="nl-layout">' +
      // 左栏:分类菜单
      '<aside class="nl-aside"><h3>分类</h3><div class="nl-menu" data-menu>' +
      '<button type="button" data-cat="" class="' + (state.filterCat === "" ? "nl-on" : "") + '">全部站点<span class="nl-n">' + state.links.length + "</span></button>" +
      cats
        .map((c) => '<button type="button" data-cat="' + escapeHtml(c) + '" class="' + (state.filterCat === c ? "nl-on" : "") + '">' + escapeHtml(c) + '<span class="nl-n">' + catCount(c) + "</span></button>")
        .join("") +
      "</div></aside>" +
      // 中栏:状态行（筛选 + 计数 + 视图切换）+ 卡片网格
      '<main class="nl-main">' +
      '<div class="nl-status">' +
      (state.filterCat ? '<span class="nl-chip">分类 · ' + escapeHtml(state.filterCat) + "</span>" : "") +
      (state.filterTag ? '<span class="nl-chip">标签 · ' + escapeHtml(state.filterTag) + "</span>" : "") +
      "<span>" + rows.length + " 个站点</span>" +
      (state.filterCat || state.filterTag ? '<button type="button" class="nl-clear" data-clear>清除筛选</button>' : "") +
      '<span class="nl-view" title="切换排列方式">' +
      '<button type="button" data-view="list" class="' + (state.view === "list" ? "nl-on" : "") + '" title="列表排列">☰</button>' +
      '<button type="button" data-view="grid" class="' + (state.view === "grid" ? "nl-on" : "") + '" title="九宫格排列">▦</button>' +
      "</span></div>" +
      '<div class="nl-grid" data-view="' + state.view + '" data-grid>' + rows.map(state.view === "grid" ? gridCardHTML : rowCardHTML).join("") + "</div>" +
      (state.loaded && rows.length === 0
        ? '<p style="text-align:center;padding:40px 0;font-size:13px;color:var(--nl-text2)">' + (state.links.length === 0 ? "站长还没有收藏站点" : "没有符合筛选条件的站点") + "</p>"
        : "") +
      "</main>" +
      // 右栏:3D 标签云
      '<aside class="nl-sphere-wrap"><h3 style="margin:2px 4px 8px;font-size:12px;font-weight:600;color:var(--nl-text2);letter-spacing:.5px">标签云 · 拖动旋转</h3><div data-sphere></div></aside>' +
      "</div>";

    bind();
  };

  // ---------- 事件绑定（每次渲染后重挂） ----------
  const bind = () => {
    const kw = box.querySelector("[data-kw]");
    if (kw) {
      kw.value = state.keyword;
      kw.addEventListener("input", () => {
        state.keyword = kw.value.trim();
        render();
        const again = box.querySelector("[data-kw]");
        again.focus();
        again.setSelectionRange(again.value.length, again.value.length);
      });
    }
    box.querySelectorAll("[data-cat]").forEach((b) =>
      b.addEventListener("click", () => {
        state.filterCat = b.dataset.cat;
        render();
      })
    );
    const clearBtn = box.querySelector("[data-clear]");
    if (clearBtn) {
      clearBtn.addEventListener("click", () => {
        state.filterCat = "";
        state.filterTag = "";
        render();
      });
    }
    box.querySelectorAll("[data-view]").forEach((b) =>
      b.addEventListener("click", () => {
        if (state.view === b.dataset.view) {
          return;
        }
        state.view = b.dataset.view;
        localStorage.setItem(viewStorageKey, state.view); // 记住偏好
        render();
      })
    );
    // 3D 标签云：点击标签切换筛选（球体随整页重建，无需局部更新）
    const sphereMount = box.querySelector("[data-sphere]");
    if (sphereMount && sphere) {
      sphere.destroy();
      sphere = null;
    }
    if (sphereMount && state.tags.length) {
      const tags = state.tags.map((t) => ({ name: t, count: tagCount(t) })).sort((a, b) => b.count - a.count);
      sphere = createTagSphere({
        mount: sphereMount,
        tags: tags,
        active: state.filterTag,
        onSelect: (name) => {
          state.filterTag = state.filterTag === name ? "" : name; // 再点一次取消
          render();
        },
      });
    }
  };

  // ---------- 初始化 ----------
  fetchPublicLinks()
    .then((data) => {
      state.links = Array.isArray(data.links) ? data.links : [];
      state.categories = Array.isArray(data.categories) ? data.categories : [];
      state.tags = Array.isArray(data.tags) ? data.tags : [];
      state.settings = data.settings || {};
      state.loaded = true;
      render();
    })
    .catch(() => {
      state.loaded = true;
      box.innerHTML = '<p style="text-align:center;padding:40px 0;font-size:13px;color:var(--nl-text2)">导航数据加载失败，请刷新重试</p>';
    });

  // 清理函数（停球体动画、移除样式与 DOM）。
  return () => {
    if (sphere) {
      sphere.destroy();
      sphere = null;
    }
    styleEl.remove();
    box.remove();
  };
}
