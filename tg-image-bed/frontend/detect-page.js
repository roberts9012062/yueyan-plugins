// marketplace-repo/tg-image-bed/frontend/detect-page.js
// TG图床 · 图片体检页（admin.page /admin/plugin-pages/tg-image-bed/detect）：
// 扫描全部已发布说说/文章的正文图片 → 分类（外部外链 / 本地 /media / 已TG）→
// 勾选转存到 TG 图床（外链走插件后端下载绕 CORS，本地图前端读流转 base64）→
// 按帖聚合替换正文 URL（GET/PUT /api/v1/admin/posts/:id，宿主权限校验兜底）。
// ctx: { container, api, user, params: {pluginId, page} }
import { cardStyle, hintStyle } from "/plugin-sdk/shared.js";

// KIND_LABEL 图片分类文案。
const KIND_LABEL = { external: "外部图片", local: "本地图片", tg: "已TG" };

// bearerHeader 主站访问令牌头（与主站前端同源逻辑：localStorage yueyan-tokens 的 access_token；
// 管理 REST 走 Authorization Bearer 而非 Cookie——v0.4.1 踩坑：缺头时 /admin/* 一律 401）。
function bearerHeader() {
  try {
    const raw = localStorage.getItem("yueyan-tokens");
    const tokens = raw ? JSON.parse(raw) : {};
    return tokens.access_token ? { Authorization: "Bearer " + tokens.access_token } : {};
  } catch (e) {
    return {};
  }
}

// siteApi 宿主 REST 调用（同源 Bearer 鉴权；剥 {code,message,data} 壳，非 0 抛错）。
async function siteApi(method, url, body) {
  const res = await fetch(url, {
    method,
    headers: { ...bearerHeader(), ...(body ? { "Content-Type": "application/json" } : {}) },
    body: body ? JSON.stringify(body) : undefined,
    credentials: "same-origin",
  });
  const json = await res.json().catch(() => ({}));
  if (!res.ok || json.code !== 0) throw new Error(json.message || "HTTP " + res.status);
  return json.data;
}

// extractImages 从正文提取图片地址（html <img> 与 markdown ![]() 双正则，Set 去重；纯函数）。
function extractImages(content) {
  const found = new Set();
  const img = /<img[^>]+src="([^"]+)"/g;
  const md = /!\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)/g;
  for (const re of [img, md]) {
    let m;
    while ((m = re.exec(content || "")) !== null) found.add(m[1]);
  }
  return Array.from(found);
}

// classify 图片分类：/media/=本地；proxy_base 前缀=已TG；其余（含相对路径）=外部。
function classify(src, tgBase) {
  if (src.startsWith("/media/")) return "local";
  if (/^https?:\/\//i.test(src)) return tgBase && src.startsWith(tgBase) ? "tg" : "external";
  return "external";
}

// blobToBase64 读为纯 base64（上传通道）。
function blobToBase64(blob) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result).split(",")[1] || "");
    reader.onerror = () => reject(new Error("读取失败"));
    reader.readAsDataURL(blob);
  });
}

// replaceAll 全量替换（split/join；纯函数）。
function replaceAll(text, from, to) {
  return from === "" || !text.includes(from) ? text : text.split(from).join(to);
}

// decodeEntities HTML 实体反转义（常用五实体；正文提取的 src 含 &amp; 等转义形态，
// 直接下载会 404，须还原成干净 URL——对齐后端 html.UnescapeString 的常用子集）。
function decodeEntities(s) {
  return s.replace(/&amp;/g, "&").replace(/&lt;/g, "<").replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"').replace(/&#39;/g, "'");
}

export default function registerPage(ctx) {
  const state = { posts: [], tgBase: "", selected: new Set(), busy: false };
  const box = document.createElement("div");
  box.style.cssText = "padding:24px;max-width:1080px;margin:0 auto";
  ctx.container.appendChild(box);

  box.innerHTML =
    '<div style="display:flex;align-items:center;gap:12px;flex-wrap:wrap">' +
    '<span style="display:inline-flex;align-items:center;justify-content:center;width:38px;height:38px;border-radius:50%;background:var(--yy-accent,#5b8cff);color:#fff;font-size:18px;font-weight:700">🔍</span>' +
    '<div style="flex:1;min-width:200px"><h1 style="font-size:18px;font-weight:700;color:var(--yy-text,#e8ecf4);line-height:1.3">TG图床 · 图片体检</h1>' +
    '<p style="' + hintStyle + '">扫描说说/文章正文图片 → 外部图与本地图一键转存 TG → 自动替换正文链接</p></div>' +
    '<a href="/admin/plugin-pages/tg-image-bed/library" style="font-size:13px;color:var(--yy-accent,#5b8cff);text-decoration:none">← 返回图库</a>' +
    "</div>" +
    '<p data-health style="margin-top:10px;padding:10px 14px;border-radius:10px;font-size:12px;' + cardStyle + '"></p>' +
    '<p style="' + hintStyle + ';margin-top:8px">扫描范围为公开时间线（推荐流接口）：私密帖与仅关注者可见帖不出现在列表中，如有需要请在对应帖子编辑页手动处理。</p>' +
    '<div style="display:flex;gap:10px;margin-top:14px;flex-wrap:wrap">' +
    '<button type="button" data-scan-btn style="height:36px;border-radius:8px;padding:0 20px;font-size:13px;color:#fff;background:var(--yy-accent,#5b8cff);border:none;cursor:pointer">扫描全部帖子</button>' +
    '<button type="button" data-transfer-btn style="height:36px;border-radius:8px;padding:0 20px;font-size:13px;color:#fff;background:#30a46c;border:none;cursor:pointer;display:none">转存选中到 TG 图床</button>' +
    '<span data-stat style="' + hintStyle + ';align-self:center"></span>' +
    "</div>" +
    '<p data-status style="' + hintStyle + ';margin:10px 2px"></p>' +
    '<div data-list style="display:flex;flex-direction:column;gap:12px"></div>';

  const $ = (sel) => box.querySelector(sel);
  const healthEl = $("[data-health]"), statusEl = $("[data-status]"), listEl = $("[data-list]");
  const scanBtn = $("[data-scan-btn]"), transferBtn = $("[data-transfer-btn]"), statEl = $("[data-stat]");
  const setStatus = (msg) => { statusEl.textContent = msg || ""; };

  // checkHealth 配对探测：拿 proxy_base（已TG 判定）+ 转存可用性。
  const checkHealth = async () => {
    healthEl.textContent = "配对检测中…";
    try {
      const r = await ctx.api.get("/storage/health");
      if (r.ok) {
        state.tgBase = String(r.worker || "").replace(/\/$/, "");
        healthEl.textContent = "✓ 配对正常 · Bot " + (r.bot || "?") + " · 频道「" + (r.chat || "?") + "」";
        healthEl.style.color = "var(--yy-text,#e8ecf4)";
      } else {
        healthEl.textContent = "✗ " + (r.error || "配对失败") + "——转存前请先在插件设置完成配对";
        healthEl.style.color = "#e5484d";
        transferBtn.style.display = "none";
      }
    } catch (e) {
      healthEl.textContent = "✗ 配对检测失败：" + String(e);
      healthEl.style.color = "#e5484d";
    }
  };

  // scanAll 分页拉时间线 → 逐帖详情提取正文图片。
  const scanAll = async () => {
    state.posts = [];
    state.selected.clear();
    scanBtn.disabled = true;
    try {
      let page = 1;
      let total = 0;
      do {
        const data = await siteApi("GET", "/api/v1/posts?page=" + page + "&page_size=50");
        total = data.total || 0;
        for (const item of data.items || []) {
          setStatus("扫描中… 第 " + page + " 页 · 帖子 #" + item.id);
          const images = await scanPost(item.id, state.tgBase);
          if (images.length > 0) state.posts.push({ id: item.id, title: postTitle(item), kind: item.post_kind, images });
        }
        page += 1;
      } while ((page - 1) * 50 < total);
      render();
      const counts = countByKind();
      setStatus("扫描完成：" + state.posts.length + " 帖含图 · 外部 " + (counts.external || 0) + " 张 · 本地 " + (counts.local || 0) + " 张 · 已TG " + (counts.tg || 0) + " 张");
    } catch (e) {
      setStatus("扫描失败：" + String(e.message || e));
    } finally {
      scanBtn.disabled = false;
    }
  };

  // scanPost 单帖详情提取图片（图片地址升序稳定排序便于重扫比对）。
  const scanPost = async (postId, tgBase) => {
    const d = await siteApi("GET", "/api/v1/posts/" + postId);
    return extractImages(d.content).map((src) => ({ src, kind: classify(src, tgBase), state: "" }));
  };

  // postTitle 帖子标题（说说取摘要前 20 字）。
  const postTitle = (item) => item.title || (item.summary || "").slice(0, 20) || ("帖子 #" + item.id);

  // countByKind 各分类图片计数。
  const countByKind = () => {
    const out = {};
    for (const p of state.posts) for (const img of p.images) out[img.kind] = (out[img.kind] || 0) + 1;
    return out;
  };

  // render 帖子/图片列表（图片勾选：非 TG 且未转存成功才可选）。
  const render = () => {
    listEl.innerHTML = "";
    for (const post of state.posts) {
      const card = document.createElement("div");
      card.style.cssText = cardStyle + ";padding:14px";
      const imgsHtml = post.images.map((img, i) => {
        const key = post.id + ":" + i;
        const pickable = img.kind !== "tg" && img.state !== "done";
        const checked = state.selected.has(key) ? "checked" : "";
        const mark = img.state === "done" ? "✓ 已转存" : img.state === "error" ? "✗ " + (img.error || "失败") : KIND_LABEL[img.kind];
        const markColor = img.state === "done" ? "#30a46c" : img.state === "error" ? "#e5484d" : "var(--yy-text-3,#9aa4bf)";
        return '<label style="display:inline-flex;flex-direction:column;gap:4px;width:120px;cursor:' + (pickable ? "pointer" : "default") + ';opacity:' + (pickable ? 1 : 0.55) + '">' +
          '<img src="' + (img.src.startsWith("/") ? img.src : img.src) + '" referrerpolicy="no-referrer" loading="lazy" style="width:120px;height:90px;object-fit:cover;border-radius:8px;border:1px solid var(--yy-border,#2a3348)">' +
          '<span style="font-size:11px;color:' + markColor + ';overflow:hidden;text-overflow:ellipsis;white-space:nowrap">' + mark + "</span>" +
          (pickable ? '<input type="checkbox" data-key="' + key + '" ' + checked + ' style="align-self:flex-start">' : "") +
          "</label>";
      }).join("");
      card.innerHTML =
        '<div style="display:flex;align-items:center;gap:8px;margin-bottom:10px">' +
        '<a href="/posts/' + post.id + '" target="_blank" style="font-size:13px;font-weight:600;color:var(--yy-text,#e8ecf4);text-decoration:none">' + post.title + "</a>" +
        '<span style="font-size:11px;padding:1px 8px;border-radius:99px;background:var(--yy-bg-2,#1a2233);color:var(--yy-text-3,#9aa4bf)">' + (post.kind === "article" ? "文章" : "说说") + " · #" + post.id + "</span></div>" +
        '<div style="display:flex;gap:10px;flex-wrap:wrap">' + imgsHtml + "</div>";
      listEl.appendChild(card);
    }
    listEl.querySelectorAll('input[type="checkbox"]').forEach((cb) => {
      cb.addEventListener("change", () => {
        if (cb.checked) state.selected.add(cb.dataset.key);
        else state.selected.delete(cb.dataset.key);
        updateTransferBtn();
      });
    });
    updateTransferBtn();
  };

  // updateTransferBtn 转存按钮可见性与文案。
  const updateTransferBtn = () => {
    transferBtn.style.display = state.selected.size > 0 ? "" : "none";
    transferBtn.textContent = "转存选中 " + state.selected.size + " 张到 TG 图床";
  };

  // transferSelected 逐张转存 → 按帖聚合替换正文。
  const transferSelected = async () => {
    if (state.busy) return;
    state.busy = true;
    transferBtn.disabled = true;
    const rewrite = new Map(); // postId → [{old,new}]
    let done = 0;
    try {
      for (const post of state.posts) {
        for (let i = 0; i < post.images.length; i++) {
          const key = post.id + ":" + i;
          if (!state.selected.has(key)) continue;
          const img = post.images[i];
          setStatus("转存中… " + (done + 1) + "/" + state.selected.size + " · " + img.src.slice(0, 60));
          try {
            const tgUrl = await transferOne(img);
            img.state = "done";
            done += 1;
            if (!rewrite.has(post.id)) rewrite.set(post.id, []);
            rewrite.get(post.id).push({ old: img.src, new: tgUrl });
            state.selected.delete(key);
          } catch (e) {
            img.state = "error";
            img.error = String(e.message || e).slice(0, 40);
          }
          render();
        }
      }
      for (const [postId, pairs] of rewrite) {
        setStatus("替换正文… 帖子 #" + postId);
        await rewritePost(postId, pairs);
      }
      setStatus("完成：转存 " + done + " 张，正文已替换 " + rewrite.size + " 帖" + (state.selected.size ? "（失败 " + state.selected.size + " 张）" : ""));
    } catch (e) {
      setStatus("中断：" + String(e.message || e) + "（已成功的图不回滚，可重试剩余）");
    } finally {
      state.busy = false;
      transferBtn.disabled = false;
    }
  };

  // transferOne 单图转存：本地图同源读取→/manage/upload；外链图→/manage/transfer（后端下载，URL 反转义后传出）。
  const transferOne = async (img) => {
    const abs = decodeEntities(img.src.startsWith("http") ? img.src : new URL(img.src, location.origin).href);
    if (img.kind === "local") {
      const blob = await fetch(abs, { credentials: "same-origin" }).then((r) => {
        if (!r.ok) throw new Error("读取本地图失败 HTTP " + r.status);
        return r.blob();
      });
      const name = img.src.split("/").pop() || "site.png";
      const extOk = /\.(jpe?g|png|gif|webp)$/i.test(name);
      if (!extOk) throw new Error("本地文件名无图片扩展名");
      const mime = blob.type && /^image\/(jpeg|png|gif|webp)$/i.test(blob.type)
        ? blob.type
        : name.toLowerCase().endsWith(".png") ? "image/png" : name.toLowerCase().endsWith(".gif") ? "image/gif" : name.toLowerCase().endsWith(".webp") ? "image/webp" : "image/jpeg";
      const r = await ctx.api.post("/manage/upload", { filename: name, mime, content_b64: await blobToBase64(blob) });
      if (r.error) throw new Error(r.error);
      return r.url;
    }
    const r = await ctx.api.post("/manage/transfer", { url: abs });
    if (r.error) throw new Error(r.error);
    return r.url;
  };

  // rewritePost 全量回写正文（原样带回其余字段；URL 替换含 &amp; 转义变体）。
  const rewritePost = async (postId, pairs) => {
    const d = await siteApi("GET", "/api/v1/admin/posts/" + postId);
    let content = d.content;
    for (const pair of pairs) {
      content = replaceAll(content, pair.old, pair.new);
      content = replaceAll(content, pair.old.replace(/&/g, "&amp;"), pair.new);
    }
    if (content === d.content) throw new Error("正文替换未命中（帖子 #" + postId + "）");
    await siteApi("PUT", "/api/v1/admin/posts/" + postId, {
      title: d.title,
      content,
      content_format: d.content_format,
      tags: d.tags || [],
      media_ids: (d.media || []).map((m) => m.id),
      visibility: d.visibility,
      status: d.status === "draft" ? "draft" : "published",
    });
  };

  scanBtn.addEventListener("click", () => void scanAll());
  transferBtn.addEventListener("click", () => void transferSelected());
  void checkHealth();
}
