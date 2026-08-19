// marketplace-repo/image-cdn/frontend/library-page.js
// CF图床 · 后台图片管理页（admin.page /admin/plugin-pages/image-cdn/library）：
//   网格浏览（R2 对象直链缩略图）、单选/批量删除、点击/拖拽文件/拖拽文件夹上传
//   （上传经插件 /manage/upload：服务端压缩 + 直存 R2）。
// ctx: { container, api, user, params: {pluginId, page} }
import { escapeHtml, cardStyle, hintStyle } from "/plugin-sdk/shared.js";

// formatSize 字节数人性化（纯函数）。
function formatSize(bytes) {
  if (!bytes || bytes <= 0) return "0 B";
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1048576) return (bytes / 1024).toFixed(1) + " KB";
  return (bytes / 1048576).toFixed(1) + " MB";
}

// readAsBase64 文件读为 base64（拖拽/选择上传通道；Promise 封装）。
function readAsBase64(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result).split(",")[1] || "");
    reader.onerror = () => reject(new Error("读取失败：" + file.name));
    reader.readAsDataURL(file);
  });
}

// collectFilesFromDrop 从 drop 事件收集图片文件（含文件夹：webkitGetAsEntry 递归）。
async function collectFilesFromDrop(dataTransfer) {
  const out = [];
  const items = dataTransfer.items ? Array.from(dataTransfer.items) : [];
  const entries = items.map((it) => (it.webkitGetAsEntry ? it.webkitGetAsEntry() : null)).filter(Boolean);
  if (entries.length === 0) {
    return Array.from(dataTransfer.files || []).filter(isImage);
  }
  const walk = async (entry) => {
    if (entry.isFile) {
      const file = await new Promise((resolve, reject) => entry.file(resolve, reject));
      if (isImage(file)) out.push(file);
      return;
    }
    if (entry.isDirectory) {
      const reader = entry.createReader();
      const children = await new Promise((resolve, reject) => reader.readEntries(resolve, reject));
      for (const child of children) await walk(child);
    }
  };
  for (const entry of entries) await walk(entry);
  return out;
}

// isImage 图片类型判定（与插件压缩白名单一致；纯函数）。
function isImage(file) {
  return /^image\/(jpeg|png|gif|webp)$/i.test(file.type || "");
}

export default function registerPage(ctx) {
  const state = { objects: [], cursor: "", selected: new Set(), loading: false, uploading: false };
  const box = document.createElement("div");
  box.style.cssText = "padding:24px;max-width:1080px;margin:0 auto";
  ctx.container.appendChild(box);

  box.innerHTML =
    '<div style="display:flex;align-items:center;gap:12px;flex-wrap:wrap">' +
    '<span style="display:inline-flex;align-items:center;justify-content:center;width:38px;height:38px;border-radius:50%;background:var(--yy-accent,#5b8cff);color:#fff;font-size:18px;font-weight:700">🖼</span>' +
    '<div style="flex:1;min-width:200px"><h1 style="font-size:18px;font-weight:700;color:var(--yy-text,#e8ecf4);line-height:1.3">CF图床 · 图片管理</h1>' +
    '<p style="' + hintStyle + '">上传直达 Cloudflare R2 · 服务端压缩 · 删除即时生效</p></div>' +
    '<span data-count style="' + hintStyle + '"></span>' +
    '<button type="button" data-delete-btn style="height:36px;border-radius:8px;padding:0 16px;font-size:13px;color:#fff;background:#e5484d;border:none;cursor:pointer">删除选中</button>' +
    '</div>' +
    '<div data-drop data-dragging="0" style="margin-top:14px;' + cardStyle + ';padding:18px;border:1.5px dashed var(--yy-border,#2a3348);border-radius:12px;text-align:center;transition:border-color .15s">' +
    '<p style="font-size:13px;color:var(--yy-text,#e8ecf4);margin:0">点击选择 或 拖拽图片/文件夹 到此处上传</p>' +
    '<p style="' + hintStyle + ';margin:6px 0 12px">支持多选 · 文件夹（递归收集图片）· 服务端自动压缩（参数在插件设置）</p>' +
    '<button type="button" data-pick-btn style="height:36px;border-radius:8px;padding:0 20px;font-size:13px;color:#fff;background:var(--yy-accent,#5b8cff);border:none;cursor:pointer">选择图片</button>' +
    '<input data-file-input type="file" accept="image/*" multiple style="display:none">' +
    '</div>' +
    '<p data-status style="' + hintStyle + ';margin:10px 2px"></p>' +
    '<div data-grid style="display:grid;grid-template-columns:repeat(auto-fill,minmax(150px,1fr));gap:12px"></div>' +
    '<div style="margin-top:16px;text-align:center"><button type="button" data-more-btn style="height:36px;border-radius:8px;padding:0 20px;font-size:13px;color:var(--yy-text,#e8ecf4);background:transparent;border:1px solid var(--yy-border,#2a3348);cursor:pointer;display:none">加载更多</button></div>';

  const $ = (sel) => box.querySelector(sel);
  const grid = $("[data-grid]"), statusEl = $("[data-status]"), dropZone = $("[data-drop]");
  const fileInput = $("[data-file-input]"), moreBtn = $("[data-more-btn]"), countEl = $("[data-count]");

  // setStatus 状态行文案。
  const setStatus = (msg) => { statusEl.textContent = msg || ""; };

  // render 网格渲染（选中态/尺寸/删除按钮）。
  const render = () => {
    countEl.textContent = state.objects.length + " 张" + (state.selected.size ? "（选中 " + state.selected.size + "）" : "");
    grid.innerHTML = "";
    for (const obj of state.objects) {
      const cell = document.createElement("div");
      const sel = state.selected.has(obj.key);
      cell.style.cssText = "position:relative;border-radius:10px;overflow:hidden;border:2px solid " + (sel ? "var(--yy-accent,#5b8cff)" : "var(--yy-border,#2a3348)") + ";cursor:pointer";
      const img = document.createElement("img");
      img.src = obj.url;
      img.loading = "lazy";
      img.alt = obj.key;
      img.style.cssText = "width:100%;aspect-ratio:1;object-fit:cover;display:block";
      const meta = document.createElement("div");
      meta.style.cssText = "padding:6px 8px;font-size:11px;color:var(--yy-text-3,#8b94a8);background:var(--yy-bg-2,#1c2436)";
      meta.textContent = formatSize(obj.size) + " · " + (obj.uploaded || "").slice(0, 10);
      cell.appendChild(img);
      cell.appendChild(meta);
      cell.addEventListener("click", () => {
        if (state.selected.has(obj.key)) state.selected.delete(obj.key);
        else state.selected.add(obj.key);
        render();
      });
      grid.appendChild(cell);
    }
  };

  // load 图片列表（分页追加）。
  const load = async (append) => {
    if (state.loading) return;
    state.loading = true;
    setStatus(append ? "加载中…" : "正在拉取图库…");
    try {
      const r = await ctx.api.post("/manage/list", { cursor: append ? state.cursor : "" });
      if (r.error) { setStatus("加载失败：" + r.error); return; }
      state.objects = append ? state.objects.concat(r.objects || []) : (r.objects || []);
      state.cursor = r.cursor || "";
      moreBtn.style.display = state.cursor ? "inline-block" : "none";
      render();
      setStatus(state.objects.length === 0 ? "图库为空——上传第一批图片吧" : "");
    } catch (e) {
      setStatus("加载失败：" + String(e));
    } finally {
      state.loading = false;
    }
  };

  // uploadFiles 上传收集到的图片文件（逐个：读 base64 → /manage/upload）。
  const uploadFiles = async (files) => {
    const images = files.filter(isImage);
    if (images.length === 0) { setStatus("未发现图片文件（支持 jpg/png/gif/webp）"); return; }
    state.uploading = true;
    let ok = 0;
    for (const file of images) {
      setStatus("上传中 " + (ok + 1) + "/" + images.length + "：" + file.name);
      try {
        const b64 = await readAsBase64(file);
        const r = await ctx.api.post("/manage/upload", { filename: file.name, mime: file.type, content_b64: b64 });
        if (r.error) { setStatus("「" + file.name + "」失败：" + r.error); continue; }
        ok++;
      } catch (e) {
        setStatus("「" + file.name + "」失败：" + String(e));
      }
    }
    state.uploading = false;
    setStatus("✓ 上传完成 " + ok + "/" + images.length + " 张");
    await load(false);
  };

  // 删除选中（确认后批量）
  $("[data-delete-btn]").addEventListener("click", async () => {
    const keys = Array.from(state.selected);
    if (keys.length === 0) { setStatus("先点击图片选中再删除"); return; }
    if (!window.confirm("确认删除选中的 " + keys.length + " 张图片？删除后不可恢复。")) return;
    setStatus("删除中…");
    try {
      const r = await ctx.api.post("/manage/delete", { keys });
      state.selected.clear();
      setStatus("✓ 已删除 " + (r.deleted ?? 0) + "/" + keys.length + " 张");
      await load(false);
    } catch (e) {
      setStatus("删除失败：" + String(e));
    }
  });

  // 点击选择上传
  $("[data-pick-btn]").addEventListener("click", () => fileInput.click());
  fileInput.addEventListener("change", () => {
    void uploadFiles(Array.from(fileInput.files || []));
    fileInput.value = "";
  });

  // 拖拽上传（文件/文件夹；进入高亮）
  dropZone.addEventListener("dragover", (e) => { e.preventDefault(); dropZone.setAttribute("data-dragging", "1"); dropZone.style.borderColor = "var(--yy-accent,#5b8cff)"; });
  dropZone.addEventListener("dragleave", () => { dropZone.setAttribute("data-dragging", "0"); dropZone.style.borderColor = "var(--yy-border,#2a3348)"; });
  dropZone.addEventListener("drop", async (e) => {
    e.preventDefault();
    dropZone.style.borderColor = "var(--yy-border,#2a3348)";
    if (state.uploading) return;
    const files = await collectFilesFromDrop(e.dataTransfer);
    void uploadFiles(files);
  });

  moreBtn.addEventListener("click", () => void load(true));
  void load(false);
}
