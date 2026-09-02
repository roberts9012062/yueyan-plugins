// nav-links/frontend/private-settings.js
// 精品导航 · 私有导航设置弹层（admin-page 工具栏「私有设置」调用）：
//   访问方式单选（仅自己可见 self / 密码访问 password）+ 访问密码设置（留空=不修改）
//   + 私有页标题/副标题。保存调插件 POST /private/config（密码哈希落插件数据目录）。
// openPrivateSettings({ api, onSaved })：关闭时（无论是否保存）调用 onSaved() 刷新外层。
import { escapeHtml } from "/plugin-sdk/shared.js";

const colorText = "color:var(--yy-text,#e8ecf4)";
const colorText2 = "color:var(--yy-text-2,#9aa6bc)";
const inputStyle =
  "height:36px;width:100%;border-radius:8px;border:1px solid var(--yy-border,#2a3348);background:transparent;color:var(--yy-text,#e8ecf4);padding:0 12px;font-size:13px;outline:none;box-sizing:border-box";
const labelStyle = "display:block;margin-bottom:4px;font-size:12px;" + colorText2;

// modeDesc 两种访问方式的说明文案（纯函数）。
const modeDesc = {
  self: "仅站长登录后可见——前台私有页对其他访客显示「仅站长可见」。",
  password: "访客输入访问密码解锁浏览（7 天内免重复输入；修改密码后需重新解锁）。",
};

export function openPrivateSettings(opts) {
  const api = opts.api;
  const state = { mode: "self", hasPassword: false, title: "", subtitle: "" };

  const overlay = document.createElement("div");
  overlay.style.cssText = "position:fixed;inset:0;z-index:60;display:flex;align-items:center;justify-content:center;background:rgba(10,14,22,.55);padding:16px";
  const card = document.createElement("div");
  card.style.cssText =
    "width:100%;max-width:480px;max-height:90vh;overflow-y:auto;border-radius:14px;border:1px solid var(--yy-border,#2a3348);background:var(--yy-elevated,#fff);padding:20px";
  overlay.appendChild(card);
  document.body.appendChild(overlay);

  const skeleton =
    '<h2 style="font-size:16px;font-weight:700;' + colorText + '">私有导航设置</h2>' +
    '<p style="margin-top:4px;font-size:12px;' + colorText2 + '">可见性为「私有」的站点展示在前台「私有导航」页，此处控制该页面的访问方式。</p>' +
    '<p style="margin-top:20px;font-size:13px;' + colorText2 + '">正在读取配置…</p>';
  card.innerHTML = skeleton;

  const close = () => {
    overlay.remove();
    if (typeof opts.onSaved === "function") opts.onSaved();
  };

  // renderForm 配置表单（cfg：插件端 meta 口径 {mode,has_password,title,subtitle,count}）。
  const renderForm = (cfg) => {
    state.mode = cfg.mode === "password" ? "password" : "self";
    state.hasPassword = Boolean(cfg.has_password);
    state.title = cfg.title || "私有导航";
    state.subtitle = cfg.subtitle || "";
    card.innerHTML =
      '<h2 style="font-size:16px;font-weight:700;' + colorText + '">私有导航设置</h2>' +
      '<p style="margin-top:4px;font-size:12px;' + colorText2 + '">可见性为「私有」的站点展示在前台「私有导航」页（当前 ' + (cfg.count || 0) + ' 个），此处控制该页面的访问方式。</p>' +
      // 访问方式
      '<div style="margin-top:14px"><label style="' + labelStyle + '">访问方式</label>' +
      '<div style="display:flex;gap:8px">' +
      '<button type="button" data-mode="self" style="flex:1;height:36px;border-radius:8px;cursor:pointer;font-size:13px">👤 仅自己可见</button>' +
      '<button type="button" data-mode="password" style="flex:1;height:36px;border-radius:8px;cursor:pointer;font-size:13px">🔑 密码访问</button></div>' +
      '<p data-mode-desc style="margin:6px 0 0;font-size:11px;' + colorText2 + '"></p></div>' +
      // 密码（password 模式展示；已设密码时留空表示不修改）
      '<div data-pw-box style="margin-top:10px;display:none"><label style="' + labelStyle + '">' +
      (state.hasPassword ? "访问密码（留空 = 保持现有密码）" : "访问密码 *（首次启用密码访问需设置，6-64 位）") + "</label>" +
      '<input data-pw type="password" placeholder="输入访问密码…" autocomplete="new-password" style="' + inputStyle + '"></div>' +
      // 私有页文案
      '<div style="margin-top:10px"><label style="' + labelStyle + '">私有页标题</label>' +
      '<input data-title type="text" maxlength="30" placeholder="私有导航" style="' + inputStyle + '"></div>' +
      '<div style="margin-top:10px"><label style="' + labelStyle + '">私有页副标题（可选，≤60 字）</label>' +
      '<input data-subtitle type="text" maxlength="60" placeholder="仅对站长与获准访客可见的收藏站点" style="' + inputStyle + '"></div>' +
      // 底部
      '<div style="margin-top:16px;display:flex;justify-content:flex-end;gap:8px">' +
      '<p data-err style="flex:1;font-size:12px;color:#ef4444;align-self:center"></p>' +
      '<button type="button" data-cancel style="height:34px;padding:0 16px;border-radius:999px;border:1px solid var(--yy-border,#2a3348);background:transparent;font-size:13px;' + colorText + ';cursor:pointer">关闭</button>' +
      '<button type="button" data-save style="height:34px;padding:0 18px;border-radius:999px;border:none;background:var(--yy-accent,#6366f1);color:#fff;font-size:13px;font-weight:600;cursor:pointer">保存</button></div>';

    const pwBox = card.querySelector("[data-pw-box]");
    const pwEl = card.querySelector("[data-pw]");
    const titleEl = card.querySelector("[data-title]");
    const subtitleEl = card.querySelector("[data-subtitle]");
    const modeDescEl = card.querySelector("[data-mode-desc]");
    const errEl = card.querySelector("[data-err]");
    titleEl.value = state.title;
    subtitleEl.value = state.subtitle;

    // renderMode 高亮当前访问方式并联动密码框显隐。
    const renderMode = () => {
      card.querySelectorAll("[data-mode]").forEach((btn) => {
        const on = btn.dataset.mode === state.mode;
        btn.style.border = on ? "none" : "1px solid var(--yy-border,#2a3348)";
        btn.style.background = on ? "var(--yy-accent,#6366f1)" : "transparent";
        btn.style.color = on ? "#fff" : "var(--yy-text,#e8ecf4)";
        btn.style.fontWeight = on ? "600" : "400";
      });
      modeDescEl.textContent = modeDesc[state.mode];
      pwBox.style.display = state.mode === "password" ? "block" : "none";
    };
    card.querySelectorAll("[data-mode]").forEach((btn) =>
      btn.addEventListener("click", () => {
        state.mode = btn.dataset.mode;
        renderMode();
      })
    );
    renderMode();

    card.querySelector("[data-cancel]").addEventListener("click", close);
    card.querySelector("[data-save]").addEventListener("click", async () => {
      errEl.textContent = "";
      try {
        const r = await api.post("/private/config", {
          mode: state.mode,
          password: pwEl.value,
          title: titleEl.value.trim() || "私有导航",
          subtitle: subtitleEl.value.trim(),
        });
        if (r.error) {
          errEl.textContent = r.error;
          return;
        }
        state.hasPassword = Boolean(r.has_password);
        pwEl.value = "";
        pwBox.querySelector("label").textContent = "访问密码（留空 = 保持现有密码）";
        errEl.style.color = "var(--yy-text-2,#9aa6bc)";
        errEl.textContent = "已保存 ✓";
        setTimeout(() => {
          errEl.textContent = "";
          errEl.style.color = "#ef4444";
        }, 1500);
      } catch (e) {
        errEl.textContent = "保存失败，请稍后重试";
      }
    });
  };

  overlay.addEventListener("click", (ev) => {
    if (ev.target === overlay) close();
  });

  api
    .get("/private/config")
    .then(renderForm)
    .catch(() => {
      card.innerHTML =
        '<h2 style="font-size:16px;font-weight:700;' + colorText + '">私有导航设置</h2>' +
        '<p style="margin-top:12px;font-size:13px;color:#ef4444">配置读取失败，请刷新页面重试。</p>';
    });
}
