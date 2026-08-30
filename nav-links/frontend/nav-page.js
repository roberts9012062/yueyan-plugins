// nav-links/frontend/nav-page.js
// 精品导航 · 前台公开页（site.page /plugins/nav-links/index，访客无需登录）：
//   分类 Tab + 标签筛选 + 搜索 + 卡片网格，展示站长收藏的精品站点。
// 数据经宿主公开桥接 GET /api/v1/nav/links（匿名可读；不经 ctx.api——其需登录）。
// ctx: { container, api, user, params: {pluginId, page} }
import { escapeHtml } from "/plugin-sdk/shared.js";

// hostHue 由域名稳定映射一个色相（无图标站点的首字母色块着色；纯函数）。
function hostHue(host) {
  let h = 0;
  for (const ch of host) {
    h = (h * 31 + ch.codePointAt(0)) % 360;
  }
  return h;
}

// 简化 fetch：公开桥接直通插件 JSON（非 {code,data} 包裹格式）。
async function fetchPublicLinks() {
  const res = await fetch("/api/v1/nav/links", { headers: { Accept: "application/json" } });
  if (!res.ok) {
    throw new Error("HTTP " + res.status);
  }
  return res.json();
}

export default function registerPage(ctx) {
  const state = { links: [], categories: [], tags: [], settings: {}, keyword: "", filterCat: "", filterTag: "", loaded: false };

  const box = document.createElement("div");
  box.className = "nav-links-site";
  ctx.container.appendChild(box);

  const chip = (active, text, key) =>
    '<button type="button" data-chip-' + key + ' style="height:30px;padding:0 14px;border-radius:999px;font-size:13px;cursor:pointer;border:1px solid ' +
    (active ? "var(--yy-accent,#6366f1)" : "var(--yy-border,#2a3348)") + ";color:" +
    (active ? "var(--yy-accent,#6366f1)" : "var(--yy-text-2,#9aa6bc)") + ';background:transparent">' + escapeHtml(text) + "</button>";

  // ---------- 渲染 ----------
  const render = () => {
    const s = state.settings;
    const rows = state.links.filter((l) => {
      if (state.filterCat && l.category !== state.filterCat) return false;
      if (state.filterTag && !(l.tags || []).includes(state.filterTag)) return false;
      if (state.keyword) {
        const kw = state.keyword.toLowerCase();
        const hay = (l.name + " " + l.url + " " + (l.description || "") + " " + (l.tags || []).join(" ")).toLowerCase();
        if (!hay.includes(kw)) return false;
      }
      return true;
    });
    const newTab = s.open_new_tab !== "off";
    const target = newTab ? "_blank" : "_self";

    const cardHTML = (l) => {
      const host = (() => {
        try {
          return new URL(l.url).hostname;
        } catch (e) {
          return l.url;
        }
      })();
      const icon = l.icon
        ? '<img src="' + escapeHtml(l.icon) + '" alt="" style="width:44px;height:44px;border-radius:10px;object-fit:contain;flex:none">'
        : '<span style="width:44px;height:44px;border-radius:10px;flex:none;display:inline-flex;align-items:center;justify-content:center;font-size:18px;font-weight:700;color:#fff;background:hsl(' + hostHue(host) + ',55%,45%)">' + escapeHtml((l.name || host).charAt(0)) + "</span>";
      return (
        '<a href="' + escapeHtml(l.url) + '" target="' + target + '"' + (newTab ? ' rel="noopener noreferrer"' : "") +
        ' data-card style="display:flex;gap:12px;padding:14px;border-radius:14px;border:1px solid var(--yy-border,#2a3348);background:var(--yy-elevated,#fff);text-decoration:none;transition:border-color .15s,transform .15s">' +
        icon +
        '<div style="flex:1;min-width:0">' +
        '<div style="display:flex;align-items:center;gap:8px">' +
        '<span style="font-size:14px;font-weight:600;color:var(--yy-text,#e8ecf4);overflow:hidden;text-overflow:ellipsis;white-space:nowrap">' + escapeHtml(l.name) + "</span>" +
        '<span style="font-size:11px;padding:1px 8px;border-radius:999px;flex:none;background:var(--yy-accent-soft,#6366f120);color:var(--yy-accent,#6366f1)">' + escapeHtml(l.category || "未分类") + "</span></div>" +
        (l.description ? '<p style="margin-top:4px;font-size:12px;color:var(--yy-text-2,#9aa6bc);line-height:1.5;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden">' + escapeHtml(l.description) + "</p>" : "") +
        (l.tags && l.tags.length
          ? '<div style="margin-top:6px;display:flex;gap:4px;flex-wrap:wrap">' +
            l.tags.map((t) => '<span style="font-size:11px;padding:1px 8px;border-radius:999px;border:1px solid var(--yy-border,#2a3348);color:var(--yy-text-2,#9aa6bc)">' + escapeHtml(t) + "</span>").join("") +
            "</div>"
          : "") +
        "</div></a>"
      );
    };

    box.innerHTML =
      '<div style="text-align:center;padding:12px 0 20px">' +
      '<h1 style="font-size:26px;font-weight:800;color:var(--yy-text,#e8ecf4);letter-spacing:.5px">' + escapeHtml(s.page_title || "精品导航") + "</h1>" +
      '<p style="margin-top:6px;font-size:13px;color:var(--yy-text-2,#9aa6bc)">' + escapeHtml(s.page_subtitle || "收藏的优质站点") + "</p></div>" +
      // 搜索
      '<div style="max-width:420px;margin:0 auto 14px">' +
      '<input data-kw type="text" placeholder="搜索站点…"' +
      ' style="height:40px;width:100%;border-radius:999px;border:1px solid var(--yy-border,#2a3348);background:var(--yy-elevated,#fff);color:var(--yy-text,#e8ecf4);padding:0 18px;font-size:13px;outline:none;box-sizing:border-box;text-align:center"></div>' +
      // 分类 Tab
      (state.categories.length
        ? '<div data-cats style="display:flex;gap:8px;flex-wrap:wrap;justify-content:center;margin-bottom:8px">' +
          chip(!state.filterCat, "全部", "cat-all") +
          state.categories.map((c) => chip(state.filterCat === c, c, "cat")).join("") +
          "</div>"
        : "") +
      // 标签筛选
      (state.tags.length
        ? '<div data-tags style="display:flex;gap:6px;flex-wrap:wrap;justify-content:center;margin-bottom:16px;max-height:66px;overflow:hidden">' +
          state.tags.map((t) => chip(state.filterTag === t, "# " + t, "tag")).join("") +
          "</div>"
        : "") +
      // 网格
      '<div data-grid style="display:grid;grid-template-columns:repeat(auto-fill,minmax(280px,1fr));gap:10px">' +
      rows.map(cardHTML).join("") +
      "</div>" +
      (state.loaded && rows.length === 0
        ? '<p style="text-align:center;padding:40px 0;font-size:13px;color:var(--yy-text-2,#9aa6bc)">' +
          (state.links.length === 0 ? "站长还没有收藏站点" : "没有符合筛选条件的站点") +
          "</p>"
        : "") +
      (state.loaded && rows.length > 0
        ? '<p style="text-align:center;padding:20px 0 6px;font-size:11px;color:var(--yy-text-2,#9aa6bc)">共 ' + rows.length + " 个站点</p>"
        : "");

    bind();
  };

  // ---------- 事件绑定（每次 render 后重挂） ----------
  const bind = () => {
    const kw = box.querySelector("[data-kw]");
    if (kw) {
      kw.value = state.keyword;
      kw.addEventListener("input", () => {
        state.keyword = kw.value.trim();
        // 输入只重绘网格会复杂化——整页重绘并保持焦点
        render();
        const again = box.querySelector("[data-kw]");
        again.focus();
        again.setSelectionRange(again.value.length, again.value.length);
      });
    }
    box.querySelectorAll("[data-card]").forEach((el) => {
      el.addEventListener("mouseover", () => {
        el.style.borderColor = "var(--yy-accent,#6366f1)";
        el.style.transform = "translateY(-2px)";
      });
      el.addEventListener("mouseout", () => {
        el.style.borderColor = "var(--yy-border,#2a3348)";
        el.style.transform = "";
      });
    });
    box.querySelectorAll("[data-chip-cat-all]").forEach((b) =>
      b.addEventListener("click", () => {
        state.filterCat = "";
        render();
      })
    );
    box.querySelectorAll("[data-chip-cat]").forEach((b, i) =>
      b.addEventListener("click", () => {
        const c = state.categories[i];
        state.filterCat = state.filterCat === c ? "" : c; // 再点一次取消
        render();
      })
    );
    box.querySelectorAll("[data-chip-tag]").forEach((b, i) =>
      b.addEventListener("click", () => {
        const t = state.tags[i];
        state.filterTag = state.filterTag === t ? "" : t;
        render();
      })
    );
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
      box.innerHTML =
        '<p style="text-align:center;padding:40px 0;font-size:13px;color:var(--yy-text-2,#9aa6bc)">导航数据加载失败，请刷新重试</p>';
    });

  return () => box.remove();
}
