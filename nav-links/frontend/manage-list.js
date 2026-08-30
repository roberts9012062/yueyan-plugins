// nav-links/frontend/manage-list.js
// 分类/标签管理弹层（admin-page 调用）：列表展示（使用计数）+ 添加 + 行内重命名 + 删除。
// 通用组件：增/改/删操作由调用方注入（add/rename/remove 返回 Promise<{error?}>），
// 本模块只负责 UI 与本地列表维护，操作成功后回调 onChanged() 刷新外部数据。
import { escapeHtml } from "/plugin-sdk/shared.js";

const colorText = "color:var(--yy-text,#e8ecf4)";
const colorText2 = "color:var(--yy-text-2,#9aa6bc)";
const inputStyle =
  "height:34px;border-radius:8px;border:1px solid var(--yy-border,#2a3348);background:transparent;color:var(--yy-text,#e8ecf4);padding:0 10px;font-size:13px;outline:none;box-sizing:border-box";

export function openTaxonomyManager(opts) {
  const items = opts.items.slice(); // [{name, count}] 本地副本

  const overlay = document.createElement("div");
  overlay.style.cssText = "position:fixed;inset:0;z-index:60;display:flex;align-items:center;justify-content:center;background:rgba(10,14,22,.55);padding:16px";
  const card = document.createElement("div");
  card.style.cssText =
    "width:100%;max-width:440px;max-height:80vh;display:flex;flex-direction:column;border-radius:14px;border:1px solid var(--yy-border,#2a3348);background:var(--yy-elevated,#fff);padding:18px";
  overlay.appendChild(card);
  document.body.appendChild(overlay);

  card.innerHTML =
    '<div style="display:flex;align-items:center;justify-content:space-between">' +
    '<h2 style="font-size:15px;font-weight:700;' + colorText + '">' + escapeHtml(opts.title) + "</h2>" +
    '<button type="button" data-close style="height:28px;width:28px;border-radius:8px;border:1px solid var(--yy-border,#2a3348);background:transparent;' + colorText2 + ';cursor:pointer">×</button></div>' +
    // 添加行
    '<div style="margin-top:12px;display:flex;gap:8px">' +
    '<input data-new-name type="text" placeholder="新' + escapeHtml(opts.unitLabel) + '名称…" maxlength="30" style="' + inputStyle + ';flex:1">' +
    '<button type="button" data-add style="height:34px;padding:0 14px;border-radius:8px;border:none;background:var(--yy-accent,#6366f1);color:#fff;font-size:13px;font-weight:600;cursor:pointer">添加</button></div>' +
    '<p data-msg style="margin-top:6px;min-height:16px;font-size:12px;' + colorText2 + '"></p>' +
    // 列表
    '<div data-list style="margin-top:4px;overflow-y:auto;display:flex;flex-direction:column;gap:6px;max-height:46vh"></div>' +
    '<p style="margin-top:10px;font-size:11px;' + colorText2 + '">删除' + escapeHtml(opts.unitLabel) + '会同步影响使用它的站点；重命名会级联更新全部站点。</p>';

  const listEl = card.querySelector("[data-list]");
  const msgEl = card.querySelector("[data-msg]");
  const nameEl = card.querySelector("[data-new-name]");

  const close = () => overlay.remove();
  const say = (text) => (msgEl.textContent = text);

  // renderRow 渲染一行（浏览态或行内重命名编辑态由调用处切换——编辑态整行重绘）。
  const renderList = () => {
    if (items.length === 0) {
      listEl.innerHTML = '<p style="text-align:center;padding:18px 0;font-size:13px;' + colorText2 + '">暂无' + escapeHtml(opts.unitLabel) + '</p>';
      return;
    }
    listEl.innerHTML = items
      .map(
        (it, i) =>
          '<div style="display:flex;align-items:center;gap:8px;padding:8px 10px;border-radius:10px;border:1px solid var(--yy-border,#2a3348)">' +
          '<span style="flex:1;min-width:0;font-size:13px;' + colorText + ';overflow:hidden;text-overflow:ellipsis;white-space:nowrap">' + escapeHtml(it.name) + "</span>" +
          '<span style="flex:none;font-size:11px;' + colorText2 + '">' + it.count + " 个站点</span>" +
          '<button type="button" data-rename="' + i + '" style="height:26px;padding:0 10px;border-radius:8px;border:1px solid var(--yy-border,#2a3348);background:transparent;font-size:12px;' + colorText + ';cursor:pointer">重命名</button>' +
          '<button type="button" data-del="' + i + '" style="height:26px;padding:0 10px;border-radius:8px;border:1px solid rgba(239,68,68,.4);background:transparent;font-size:12px;color:#ef4444;cursor:pointer">删除</button></div>'
      )
      .join("");
    listEl.querySelectorAll("[data-rename]").forEach((btn) =>
      btn.addEventListener("click", () => startRename(Number(btn.dataset.rename)))
    );
    listEl.querySelectorAll("[data-del]").forEach((btn) =>
      btn.addEventListener("click", () => doRemove(Number(btn.dataset.del)))
    );
  };

  // startRename 行内重命名：把该行替换为输入框 + 确认/取消。
  const startRename = (index) => {
    const it = items[index];
    const row = listEl.children[index];
    if (!row || !it) {
      return;
    }
    row.innerHTML =
      '<input data-rename-to type="text" maxlength="30" style="' + inputStyle + ';flex:1" value="' + escapeHtml(it.name) + '">' +
      '<button type="button" data-rename-ok style="height:26px;padding:0 10px;border-radius:8px;border:none;background:var(--yy-accent,#6366f1);color:#fff;font-size:12px;cursor:pointer">确定</button>' +
      '<button type="button" data-rename-cancel style="height:26px;padding:0 10px;border-radius:8px;border:1px solid var(--yy-border,#2a3348);background:transparent;font-size:12px;' + colorText + ';cursor:pointer">取消</button>';
    const input = row.querySelector("[data-rename-to]");
    input.focus();
    input.select();
    row.querySelector("[data-rename-cancel]").addEventListener("click", renderList);
    row.querySelector("[data-rename-ok]").addEventListener("click", async () => {
      const to = input.value.trim();
      if (!to) {
        say("请输入新名称");
        return;
      }
      const r = await opts.rename(it.name, to);
      if (r && r.error) {
        say(r.error);
        return;
      }
      say("已重命名" + (r && r.affected ? "（更新 " + r.affected + " 个站点）" : ""));
      items[index] = { name: to, count: it.count };
      renderList();
      if (typeof opts.onChanged === "function") opts.onChanged();
    });
    input.addEventListener("keydown", (ev) => {
      if (ev.key === "Enter") row.querySelector("[data-rename-ok]").click();
      if (ev.key === "Escape") renderList();
    });
  };

  // doRemove 删除（confirm 提示影响范围）。
  const doRemove = async (index) => {
    const it = items[index];
    if (!it) {
      return;
    }
    const effect = it.count > 0 ? (opts.unitLabel === "分类" ? "，其 " + it.count + " 个站点将变为未分类" : "，将从 " + it.count + " 个站点移除") : "";
    if (!window.confirm("确定删除" + opts.unitLabel + "「" + it.name + "」" + effect + "？")) {
      return;
    }
    const r = await opts.remove(it.name);
    if (r && r.error) {
      say(r.error);
      return;
    }
    say("已删除");
    items.splice(index, 1);
    renderList();
    if (typeof opts.onChanged === "function") opts.onChanged();
  };

  // doAdd 添加。
  const doAdd = async () => {
    const name = nameEl.value.trim();
    if (!name) {
      say("请输入名称");
      return;
    }
    const r = await opts.add(name);
    if (r && r.error) {
      say(r.error);
      return;
    }
    say("已添加「" + name + "」");
    items.push({ name, count: 0 });
    nameEl.value = "";
    renderList();
    if (typeof opts.onChanged === "function") opts.onChanged();
  };

  card.querySelector("[data-add]").addEventListener("click", doAdd);
  card.querySelector("[data-close]").addEventListener("click", close);
  nameEl.addEventListener("keydown", (ev) => {
    if (ev.key === "Enter") doAdd();
  });
  overlay.addEventListener("click", (ev) => {
    if (ev.target === overlay) close();
  });

  renderList();
  return close;
}
