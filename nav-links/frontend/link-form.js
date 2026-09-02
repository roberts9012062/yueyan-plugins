// nav-links/frontend/link-form.js
// 精品导航 · 添加/编辑表单（admin-page 调用的弹层模块）：
//   URL 失焦自动抓取图标（预览 + 重抓）+ AI 智能分类/标签（模型取自站点 AI 配置）。
// openLinkForm({ api, initial, categories, onSaved })：initial 为 null 表示新增；
// 保存成功后调用 onSaved() 刷新列表并自动关闭。导出普通函数（非页面契约）。
import { escapeHtml } from "/plugin-sdk/shared.js";

const colorText = "color:var(--yy-text,#e8ecf4)";
const colorText2 = "color:var(--yy-text-2,#9aa6bc)";
const inputStyle =
  "height:36px;width:100%;border-radius:8px;border:1px solid var(--yy-border,#2a3348);background:transparent;color:var(--yy-text,#e8ecf4);padding:0 12px;font-size:13px;outline:none;box-sizing:border-box";
const labelStyle = "display:block;margin-bottom:4px;font-size:12px;" + colorText2;

export function openLinkForm(opts) {
  const api = opts.api;
  const initial = opts.initial;
  const isEdit = Boolean(initial && initial.id);
  const form = {
    url: initial ? initial.url || "" : "",
    name: initial ? initial.name || "" : "",
    category: initial ? initial.category || "" : "",
    tags: initial ? (initial.tags || []).slice() : [],
    description: initial ? initial.description || "" : "",
    icon: initial ? initial.icon || "" : "",
    visibility: initial && initial.visibility === "private" ? "private" : "open", // 默认开放：私有条目只进私有导航页
  };
  let aiModels = [];

  const overlay = document.createElement("div");
  overlay.style.cssText = "position:fixed;inset:0;z-index:60;display:flex;align-items:center;justify-content:center;background:rgba(10,14,22,.55);padding:16px";
  const card = document.createElement("div");
  card.style.cssText =
    "width:100%;max-width:520px;max-height:90vh;overflow-y:auto;border-radius:14px;border:1px solid var(--yy-border,#2a3348);background:var(--yy-elevated,#fff);padding:20px";
  overlay.appendChild(card);
  document.body.appendChild(overlay);

  card.innerHTML =
    '<h2 style="font-size:16px;font-weight:700;' + colorText + '">' + (isEdit ? "编辑站点" : "添加站点") + "</h2>" +
    // 图标预览行
    '<div style="margin-top:14px;display:flex;align-items:center;gap:12px">' +
    '<span data-icon-box style="width:48px;height:48px;border-radius:10px;flex:none;display:inline-flex;align-items:center;justify-content:center;font-size:20px;font-weight:700;color:#fff;background:var(--yy-accent,#6366f1);overflow:hidden">' + (form.name ? escapeHtml(form.name.charAt(0)) : "？") + "</span>" +
    '<div style="flex:1"><p style="font-size:12px;' + colorText2 + '">填写地址后自动抓取图标 + AI 识别名称/分类/标签/简介（内嵌存储，前台展示不依赖外部资源）</p>' +
    '<button type="button" data-refetch style="margin-top:4px;height:26px;padding:0 10px;border-radius:999px;border:1px solid var(--yy-border,#2a3348);background:transparent;font-size:12px;' + colorText + ';cursor:pointer">重新抓取图标</button></div></div>' +
    // 字段
    '<div style="margin-top:12px"><label style="' + labelStyle + '">站点地址 *</label>' +
    '<input data-f-url type="text" placeholder="example.com 或 https://example.com" style="' + inputStyle + '"></div>' +
    '<div style="margin-top:10px"><label style="' + labelStyle + '">网站名字 *</label>' +
    '<input data-f-name type="text" placeholder="如：月言博客" maxlength="60" style="' + inputStyle + '"></div>' +
    '<div style="margin-top:10px"><label style="' + labelStyle + '">分类 *（可输入新分类，或点击下方 AI 智能分类）</label>' +
    '<input data-f-cat type="text" list="nav-cat-list" placeholder="如：开发工具" maxlength="30" style="' + inputStyle + '">' +
    '<datalist id="nav-cat-list">' + (opts.categories || []).map((c) => '<option value="' + escapeHtml(c) + '">').join("") + "</datalist></div>" +
    '<div style="margin-top:10px"><label style="' + labelStyle + '">标签（可选，回车或逗号添加，最多 10 个）</label>' +
    '<div data-tags-box style="display:flex;gap:6px;flex-wrap:wrap;margin-bottom:6px"></div>' +
    '<input data-f-tag type="text" placeholder="输入标签后回车" style="' + inputStyle + '"></div>' +
    '<div style="margin-top:10px"><label style="' + labelStyle + '">站点简介（可选，≤200 字）</label>' +
    '<textarea data-f-desc rows="2" maxlength="200" placeholder="一句话介绍这个站点…" style="' + inputStyle + ';height:auto;padding:8px 12px;resize:none"></textarea></div>' +
    // 可见性（默认开放；私有条目只出现在前台「私有导航」页）
    '<div style="margin-top:10px"><label style="' + labelStyle + '">可见性（开放站点展示在前台导航页；私有站点进入私有导航页）</label>' +
    '<div style="display:flex;gap:8px">' +
    '<button type="button" data-vis="open" style="flex:1;height:36px;border-radius:8px;cursor:pointer;font-size:13px">🌐 开放</button>' +
    '<button type="button" data-vis="private" style="flex:1;height:36px;border-radius:8px;cursor:pointer;font-size:13px">🔒 私有</button></div></div>' +
    // AI 区
    '<div style="margin-top:14px;border-radius:10px;padding:10px 12px;background:var(--yy-muted,#6366f110)">' +
    '<div style="display:flex;align-items:center;gap:8px">' +
    '<span style="font-size:12px;' + colorText2 + '">AI 智能</span>' +
    '<select data-ai-model style="' + inputStyle + ';flex:1;height:30px" disabled><option value="">（未配置 AI）</option></select>' +
    '<button type="button" data-ai-btn style="height:30px;padding:0 12px;border-radius:999px;border:none;background:var(--yy-accent,#6366f1);color:#fff;font-size:12px;font-weight:600;cursor:pointer">✨ AI 识别</button></div>' +
    '<p data-ai-status style="margin-top:6px;font-size:11px;' + colorText2 + '"></p></div>' +
    // 底部按钮
    '<div style="margin-top:16px;display:flex;justify-content:flex-end;gap:8px">' +
    '<p data-err style="flex:1;font-size:12px;color:#ef4444;align-self:center"></p>' +
    '<button type="button" data-cancel style="height:34px;padding:0 16px;border-radius:999px;border:1px solid var(--yy-border,#2a3348);background:transparent;font-size:13px;' + colorText + ';cursor:pointer">取消</button>' +
    '<button type="button" data-save style="height:34px;padding:0 18px;border-radius:999px;border:none;background:var(--yy-accent,#6366f1);color:#fff;font-size:13px;font-weight:600;cursor:pointer">保存</button></div>';

  const $ = (sel) => card.querySelector(sel);
  const urlEl = $("[data-f-url]");
  const nameEl = $("[data-f-name]");
  const catEl = $("[data-f-cat]");
  const tagInput = $("[data-f-tag]");
  const tagsBox = $("[data-tags-box]");
  const descEl = $("[data-f-desc]");
  const iconBox = $("[data-icon-box]");
  const errEl = $("[data-err]");
  const aiModelEl = $("[data-ai-model]");
  const aiBtn = $("[data-ai-btn]");
  const aiStatus = $("[data-ai-status]");

  urlEl.value = form.url;
  nameEl.value = form.name;
  catEl.value = form.category;
  descEl.value = form.description;

  const close = () => overlay.remove();

  // ---------- 图标渲染与抓取 ----------
  const renderIcon = () => {
    if (form.icon) {
      iconBox.innerHTML = '<img src="' + escapeHtml(form.icon) + '" alt="" style="width:48px;height:48px;object-fit:contain">';
    } else {
      iconBox.textContent = (form.name || "？").charAt(0);
    }
  };
  renderIcon();

  const fetchIcon = async (silent) => {
    const url = urlEl.value.trim();
    if (!url) return;
    try {
      const r = await api.post("/fetch-icon", { url });
      if (r.error) {
        if (!silent) aiStatus.textContent = "图标：" + r.error;
        return;
      }
      form.icon = r.icon;
      renderIcon();
      aiStatus.textContent = "图标已抓取（来源：" + r.source + "）";
    } catch (e) {
      if (!silent) aiStatus.textContent = "图标抓取失败，请稍后重试";
    }
  };

  const onURLBlur = async () => {
    const url = urlEl.value.trim();
    if (!url) return;
    fetchIcon(true); // 图标静默抓取（状态经 AI 区统一反馈）
    // AI 可用（模型已就绪）时自动识别四项——只放 URL 即可完成全部填写
    if (!aiModelEl.disabled && aiModelEl.value) {
      runSuggest(true);
    }
  };
  urlEl.addEventListener("blur", onURLBlur);
  $("[data-refetch]").addEventListener("click", () => fetchIcon(false));

  // ---------- 标签 chips ----------
  const renderTags = () => {
    tagsBox.innerHTML = form.tags
      .map((t, i) =>
        '<span style="display:inline-flex;align-items:center;gap:4px;padding:2px 10px;border-radius:999px;border:1px solid var(--yy-border,#2a3348);font-size:12px;' + colorText + '">' +
        escapeHtml(t) + '<button type="button" data-tag-del="' + i + '" style="border:none;background:transparent;cursor:pointer;color:inherit;font-size:12px;padding:0">×</button></span>'
      )
      .join("");
    tagsBox.querySelectorAll("[data-tag-del]").forEach((btn) =>
      btn.addEventListener("click", () => {
        form.tags.splice(Number(btn.dataset.tagDel), 1);
        renderTags();
      })
    );
  };
  renderTags();

  const addTag = (raw) => {
    const v = raw.trim().replace(/[,，]$/, "");
    if (v && !form.tags.includes(v) && form.tags.length < 10) {
      form.tags.push(v);
      renderTags();
    }
    tagInput.value = "";
  };
  tagInput.addEventListener("keydown", (ev) => {
    if (ev.key === "Enter" || ev.key === "," || ev.key === "，") {
      ev.preventDefault();
      addTag(tagInput.value);
    }
  });
  tagInput.addEventListener("blur", () => addTag(tagInput.value));

  nameEl.addEventListener("input", () => {
    form.name = nameEl.value;
    if (!form.icon) renderIcon();
  });
  catEl.addEventListener("input", () => (form.category = catEl.value));
  descEl.addEventListener("input", () => (form.description = descEl.value));

  // ---------- 可见性切换 ----------
  // renderVis 高亮当前选中项（选中=实底强调；未选中=描边弱化）。
  const renderVis = () => {
    card.querySelectorAll("[data-vis]").forEach((btn) => {
      const on = btn.dataset.vis === form.visibility;
      btn.style.border = on ? "none" : "1px solid var(--yy-border,#2a3348)";
      btn.style.background = on ? "var(--yy-accent,#6366f1)" : "transparent";
      btn.style.color = on ? "#fff" : "var(--yy-text,#e8ecf4)";
      btn.style.fontWeight = on ? "600" : "400";
    });
  };
  card.querySelectorAll("[data-vis]").forEach((btn) =>
    btn.addEventListener("click", () => {
      form.visibility = btn.dataset.vis;
      renderVis();
    })
  );
  renderVis();

  // ---------- AI 智能分类 ----------
  api
    .get("/ai/models")
    .then((r) => {
      aiModels = r.models || [];
      if (!aiModels.length) {
        aiStatus.textContent = "未配置 AI 供应商，AI 智能分类不可用（可在后台「AI 设置」配置）";
        return;
      }
      const opts = [];
      for (const m of aiModels) {
        for (const model of m.models || []) {
          opts.push('<option value="' + escapeHtml(model) + '">' + escapeHtml(m.name) + " · " + escapeHtml(model) + "</option>");
        }
      }
      aiModelEl.disabled = false;
      aiModelEl.innerHTML = opts.join("");
      aiStatus.textContent = "填写地址后自动 AI 识别名称/分类/标签/简介（也可点按钮重跑）";
    })
    .catch(() => {
      aiStatus.textContent = "AI 服务暂不可用";
    });

  // runSuggest 调 AI 识别并回填四项（auto=true 为失焦自动触发，仅状态文案不同）。
  const runSuggest = async (auto) => {
    const model = aiModelEl.value;
    if (!model) {
      if (!auto) aiStatus.textContent = "请先选择 AI 模型";
      return;
    }
    if (!urlEl.value.trim()) {
      if (!auto) aiStatus.textContent = "请先填写站点地址";
      return;
    }
    aiStatus.textContent = "AI 识别中（抓取页面 + 生成）…";
    try {
      const r = await api.post("/ai/suggest", {
        model,
        url: urlEl.value.trim(),
        name: nameEl.value.trim(),
        description: descEl.value.trim(),
      });
      if (r.error) {
        aiStatus.textContent = r.error;
        return;
      }
      if (r.name) {
        nameEl.value = r.name;
        form.name = r.name;
        if (!form.icon) renderIcon();
      }
      if (r.description) {
        descEl.value = r.description;
        form.description = r.description;
      }
      if (r.category) {
        catEl.value = r.category;
        form.category = r.category;
      }
      for (const t of r.tags || []) {
        if (!form.tags.includes(t) && form.tags.length < 10) form.tags.push(t);
      }
      renderTags();
      aiStatus.textContent = auto ? "AI 已自动识别并填充，可手动调整" : "AI 已填充，可手动调整";
    } catch (e) {
      aiStatus.textContent = "AI 请求失败，请稍后重试";
    }
  };

  aiBtn.addEventListener("click", () => runSuggest(false));

  // ---------- 保存 ----------
  $("[data-cancel]").addEventListener("click", close);
  $("[data-save]").addEventListener("click", async () => {
    errEl.textContent = "";
    const payload = {
      url: urlEl.value.trim(),
      name: nameEl.value.trim(),
      category: catEl.value.trim(),
      tags: form.tags.slice(),
      description: descEl.value.trim(),
      icon: form.icon,
      visibility: form.visibility,
    };
    try {
      const r = isEdit ? await api.post("/links/update", Object.assign({ id: initial.id }, payload)) : await api.post("/links", payload);
      if (r.error) {
        errEl.textContent = r.error;
        return;
      }
      close();
      if (typeof opts.onSaved === "function") opts.onSaved();
    } catch (e) {
      errEl.textContent = "保存失败，请稍后重试";
    }
  });

  overlay.addEventListener("click", (ev) => {
    if (ev.target === overlay) close();
  });

  return close;
}
