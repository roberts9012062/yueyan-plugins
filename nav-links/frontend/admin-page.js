// nav-links/frontend/admin-page.js
// 精品导航 · 后台管理页（admin.page /admin/plugin-pages/nav-links/admin）：
//   收藏站点列表（搜索 + 分类筛选 + 标签筛选 + 手动排序 + 增删改查入口）。
// ctx: { container, api, user, params: {pluginId, page} }
// 表单（添加/编辑 + AI 智能分类 + 自动图标）拆分在 link-form.js（保持单文件精简）。
import { escapeHtml } from "/plugin-sdk/shared.js";
import { openLinkForm } from "./link-form.js?v=1";

// 样式片段（与宿主后台 --yy-* 设计变量对齐，兜底值保证明暗主题可用）。
const colorText = "color:var(--yy-text,#e8ecf4)";
const colorText2 = "color:var(--yy-text-2,#9aa6bc)";
const cardStyle = "border-radius:12px;border:1px solid var(--yy-border,#2a3348);background:var(--yy-elevated,#fff)";
const inputStyle =
  "height:36px;border-radius:8px;border:1px solid var(--yy-border,#2a3348);background:transparent;color:var(--yy-text,#e8ecf4);padding:0 12px;font-size:13px;outline:none";

export default function registerPage(ctx) {
  const state = { links: [], categories: [], tags: [], filterCat: "", filterTag: "", keyword: "" };

  const box = document.createElement("div");
  box.className = "nav-links-admin";
  box.style.padding = "24px";
  box.style.maxWidth = "860px";
  ctx.container.appendChild(box);

  // ---------- 骨架 ----------
  box.innerHTML =
    '<div style="display:flex;align-items:center;justify-content:space-between;gap:12px">' +
    '<div style="flex:1"><h1 style="font-size:18px;font-weight:700;' + colorText + '">精品导航 · 收藏管理</h1>' +
    '<p style="font-size:12px;' + colorText2 + '">收藏的站点将展示在前台「精品导航」页面，访客无需登录即可浏览。</p></div>' +
    '<button type="button" data-add style="height:36px;padding:0 16px;border-radius:999px;border:none;background:var(--yy-accent,#6366f1);color:#fff;font-size:13px;font-weight:600;cursor:pointer">＋ 添加站点</button></div>' +
    // 筛选行
    '<div style="margin-top:16px;display:flex;gap:8px;flex-wrap:wrap;align-items:center">' +
    '<input data-kw type="text" placeholder="搜索名称 / 地址 / 简介 / 标签…" style="' + inputStyle + ';flex:1;min-width:200px">' +
    '<select data-fcat style="' + inputStyle + '"><option value="">全部分类</option></select>' +
    '<select data-ftag style="' + inputStyle + '"><option value="">全部标签</option></select>' +
    '<span data-count style="font-size:12px;' + colorText2 + '"></span></div>' +
    // 列表区
    '<div data-list style="margin-top:12px;display:flex;flex-direction:column;gap:8px"></div>' +
    '<p data-empty style="display:none;margin-top:24px;text-align:center;font-size:13px;' + colorText2 + '"></p>';

  const listEl = box.querySelector("[data-list]");
  const emptyEl = box.querySelector("[data-empty]");
  const kwEl = box.querySelector("[data-kw]");
  const fcatEl = box.querySelector("[data-fcat]");
  const ftagEl = box.querySelector("[data-ftag]");
  const countEl = box.querySelector("[data-count]");

  // ---------- 数据加载 ----------
  const load = async () => {
    const r = await ctx.api.get("/links");
    state.links = Array.isArray(r.links) ? r.links : [];
    state.categories = Array.isArray(r.categories) ? r.categories : [];
    state.tags = Array.isArray(r.tags) ? r.tags : [];
    renderFilters();
    renderList();
  };

  // ---------- 筛选下拉 ----------
  const renderFilters = () => {
    const fill = (el, values, current, allLabel) => {
      el.innerHTML =
        '<option value="">' + allLabel + "</option>" +
        values.map((v) => '<option value="' + escapeHtml(v) + '"' + (v === current ? " selected" : "") + ">" + escapeHtml(v) + "</option>").join("");
    };
    fill(fcatEl, state.categories, state.filterCat, "全部分类");
    fill(ftagEl, state.tags, state.filterTag, "全部标签");
  };

  // ---------- 列表渲染 ----------
  const visibleLinks = () =>
    state.links.filter((l) => {
      if (state.filterCat && l.category !== state.filterCat) return false;
      if (state.filterTag && !(l.tags || []).includes(state.filterTag)) return false;
      if (state.keyword) {
        const kw = state.keyword.toLowerCase();
        const hay = (l.name + " " + l.url + " " + (l.description || "") + " " + (l.tags || []).join(" ")).toLowerCase();
        if (!hay.includes(kw)) return false;
      }
      return true;
    });

  const iconHTML = (link) =>
    link.icon
      ? '<img src="' + escapeHtml(link.icon) + '" alt="" style="width:36px;height:36px;border-radius:8px;object-fit:contain;background:var(--yy-border,#2a3348);flex:none">'
      : '<span style="width:36px;height:36px;border-radius:8px;flex:none;display:inline-flex;align-items:center;justify-content:center;font-size:16px;font-weight:700;color:#fff;background:var(--yy-accent,#6366f1)">' + escapeHtml(link.name.charAt(0) || "?") + "</span>";

  const rowHTML = (link, first, last) =>
    '<div style="' + cardStyle + ';display:flex;align-items:center;gap:12px;padding:12px">' +
    iconHTML(link) +
    '<div style="flex:1;min-width:0">' +
    '<div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap">' +
    '<span style="font-size:14px;font-weight:600;' + colorText + '">' + escapeHtml(link.name) + "</span>" +
    '<span style="font-size:11px;padding:2px 8px;border-radius:999px;background:var(--yy-accent-soft,#6366f120);' + colorText2 + '">' + escapeHtml(link.category || "未分类") + "</span>" +
    (link.tags || []).map((t) => '<span style="font-size:11px;padding:2px 8px;border-radius:999px;border:1px solid var(--yy-border,#2a3348);' + colorText2 + '">' + escapeHtml(t) + "</span>").join("") +
    "</div>" +
    '<p style="margin-top:2px;font-size:12px;' + colorText2 + ';overflow:hidden;text-overflow:ellipsis;white-space:nowrap">' + escapeHtml(link.url) + "</p>" +
    (link.description ? '<p style="margin-top:2px;font-size:12px;' + colorText2 + ';overflow:hidden;text-overflow:ellipsis;white-space:nowrap">' + escapeHtml(link.description) + "</p>" : "") +
    "</div>" +
    '<div style="display:flex;gap:6px;flex:none">' +
    '<button type="button" data-up="' + link.id + '"' + (first ? " disabled" : "") + ' title="上移" style="height:28px;width:28px;border-radius:8px;border:1px solid var(--yy-border,#2a3348);background:transparent;' + colorText2 + ';cursor:pointer">↑</button>' +
    '<button type="button" data-down="' + link.id + '"' + (last ? " disabled" : "") + ' title="下移" style="height:28px;width:28px;border-radius:8px;border:1px solid var(--yy-border,#2a3348);background:transparent;' + colorText2 + ';cursor:pointer">↓</button>' +
    '<button type="button" data-edit="' + link.id + '" style="height:28px;padding:0 10px;border-radius:8px;border:1px solid var(--yy-border,#2a3348);background:transparent;font-size:12px;' + colorText + ';cursor:pointer">编辑</button>' +
    '<button type="button" data-del="' + link.id + '" style="height:28px;padding:0 10px;border-radius:8px;border:1px solid rgba(239,68,68,.4);background:transparent;font-size:12px;color:#ef4444;cursor:pointer">删除</button>' +
    "</div></div>";

  const renderList = () => {
    const rows = visibleLinks();
    countEl.textContent = "共 " + rows.length + " / " + state.links.length + " 个站点";
    if (rows.length === 0 || state.links.length === 0) {
      listEl.innerHTML = "";
      emptyEl.style.display = "block";
      emptyEl.textContent = state.links.length === 0 ? "还没有收藏站点，点击右上角「添加站点」开始" : "没有符合筛选条件的站点";
      return;
    }
    emptyEl.style.display = "none";
    listEl.innerHTML = rows.map((l, i) => rowHTML(l, i === 0, i === rows.length - 1)).join("");
    listEl.querySelectorAll("[data-edit]").forEach((btn) =>
      btn.addEventListener("click", () => {
        const link = state.links.find((l) => String(l.id) === btn.dataset.edit);
        if (link) {
          openLinkForm({ api: ctx.api, initial: link, categories: state.categories, onSaved: load });
        }
      })
    );
    listEl.querySelectorAll("[data-del]").forEach((btn) =>
      btn.addEventListener("click", async () => {
        const link = state.links.find((l) => String(l.id) === btn.dataset.del);
        if (link && window.confirm("确定删除「" + link.name + "」？")) {
          await ctx.api.post("/links/delete", { id: link.id });
          load();
        }
      })
    );
    listEl.querySelectorAll("[data-up],[data-down]").forEach((btn) =>
      btn.addEventListener("click", () => moveLink(btn.dataset.up || btn.dataset.down, btn.dataset.up ? -1 : 1))
    );
  };

  // moveLink 在全量序列中与「可见序列中的相邻项」交换位置（筛选状态下语义仍直观）。
  const moveLink = (id, dir) => {
    const rows = visibleLinks();
    const visIdx = rows.findIndex((l) => String(l.id) === String(id));
    const target = visIdx + dir;
    if (visIdx < 0 || target < 0 || target >= rows.length) return;
    const swap = rows[target];
    const fullIds = state.links.map((l) => l.id);
    const a = fullIds.findIndex((v) => v === Number(id) || String(v) === String(id));
    const b = fullIds.findIndex((v) => v === swap.id);
    if (a < 0 || b < 0) return;
    [fullIds[a], fullIds[b]] = [fullIds[b], fullIds[a]];
    ctx.api.post("/links/reorder", { ids: fullIds }).then(load);
  };

  // ---------- 事件绑定 ----------
  box.querySelector("[data-add]").addEventListener("click", () =>
    openLinkForm({ api: ctx.api, initial: null, categories: state.categories, onSaved: load })
  );
  kwEl.addEventListener("input", () => {
    state.keyword = kwEl.value.trim();
    renderList();
  });
  fcatEl.addEventListener("change", () => {
    state.filterCat = fcatEl.value;
    renderList();
  });
  ftagEl.addEventListener("change", () => {
    state.filterTag = ftagEl.value;
    renderList();
  });

  load().catch(() => {
    emptyEl.style.display = "block";
    emptyEl.textContent = "数据加载失败，请刷新页面重试";
  });

  return () => box.remove();
}
