// cmd/qq-music-plugin/frontend/login-page.js
// QQ 音乐插件 · 后台页（admin.page /admin/plugin-pages/qq-music/login）：
//   Tab 1「QQ音乐登录」：手机 QQ 扫码登录（ptlogin2），Cookie 导入备用。
//   Tab 2「我的歌单」：拉取账号歌单（含我喜欢），可试播，可设为首页背景音乐。
//   Tab 3「设置」：开启/关闭首页背景音乐（首页右下角悬浮播放器）。
// ctx: { container, api, user, params: {pluginId, page} }
// E2 去重：escapeHtml/试播/页面骨架改用宿主共享 SDK（/plugin-sdk/shared.js，同源 ESM）。
import { escapeHtml, createAudioPreview, pageChrome, cardStyle, hintStyle } from "/plugin-sdk/shared.js";

export default function registerPage(ctx) {
  const GREEN = "#31c27c"; // QQ 音乐主色（绿）
  const S = { card: cardStyle, hint: hintStyle };
  const preview = createAudioPreview(); // 歌单试播（共享控制器）

  const box = document.createElement("div");
  box.style.cssText = "padding:24px;max-width:720px;margin:0 auto";
  ctx.container.appendChild(box);

  // 骨架：标题 + Tab 导航 + 三个面板（登录/歌单/设置）——共享 pageChrome 生成
  const chrome = pageChrome({
    color: GREEN,
    icon: "Q",
    title: "QQ音乐",
    subtitle: "扫码登录 · 歌单管理 · 首页背景音乐",
    tabs: [
      { key: "login", label: "QQ音乐登录" },
      { key: "playlists", label: "我的歌单" },
      { key: "settings", label: "设置" },
    ],
  });
  box.innerHTML = chrome.html;

  const panelLogin = box.querySelector(chrome.panel("login"));
  const panelPlaylists = box.querySelector(chrome.panel("playlists"));
  const panelSettings = box.querySelector(chrome.panel("settings"));
  let pollTimer = 0;

  const stopPoll = () => { if (pollTimer) { clearTimeout(pollTimer); pollTimer = 0; } };

  // ---------- Tab 1：登录 ----------
  const renderLoggedIn = (uin) => {
    panelLogin.innerHTML =
      '<div style="' + S.card + ';padding:16px;display:flex;align-items:center;justify-content:space-between">' +
      '<div><p style="font-size:14px;font-weight:600;color:var(--yy-text,#e8ecf4)">已登录：' + escapeHtml(uin) + "</p>" +
      '<p style="' + S.hint + '">登录态已持久化；可在「我的歌单」管理歌单</p></div>' +
      '<button type="button" data-logout style="height:32px;border-radius:999px;padding:0 16px;font-size:13px;color:var(--yy-text,#e8ecf4);background:transparent;border:1px solid var(--yy-border,#2a3348);cursor:pointer">登出</button>' +
      "</div>";
    panelLogin.querySelector("[data-logout]").addEventListener("click", async () => { await ctx.api.post("/logout", {}); renderStatus(); });
  };

  // renderCookieLogin 备用登录：粘贴 y.qq.com 的 cookie 导入。
  const renderCookieLogin = () => {
    stopPoll();
    panelLogin.innerHTML =
      '<div style="' + S.card + ';padding:16px">' +
      '<p style="font-size:13px;font-weight:600;color:var(--yy-text,#e8ecf4)">导入登录 Cookie（备用）</p>' +
      '<p style="margin-top:6px;' + S.hint + '">在 <a href="https://y.qq.com/" target="_blank" style="color:var(--yy-glow,#c5d0e8)">y.qq.com</a> 扫码登录后复制整段 cookie 粘贴</p>' +
      '<textarea data-cookie rows="5" placeholder="uin=xxx; qm_keyst=xxx; qqmusic_key=xxx; login_type=1; ..." style="margin-top:10px;width:100%;border-radius:8px;border:1px solid var(--yy-border,#2a3348);padding:10px 12px;font-size:12px;color:var(--yy-text,#e8ecf4);background:var(--yy-elevated,#fff);resize:vertical"></textarea>' +
      '<div style="margin-top:10px;display:flex;align-items:center;gap:12px">' +
      '<button type="button" data-login style="height:40px;border-radius:8px;padding:0 24px;font-size:14px;font-weight:600;color:#fff;background:' + GREEN + ';border:none;cursor:pointer">导入登录</button>' +
      '<span data-login-msg style="' + S.hint + '"></span></div>' +
      '<p style="margin-top:10px;' + S.hint + '"><a href="#" data-back-qr style="color:var(--yy-glow,#c5d0e8)">返回扫码登录</a></p></div>';
    const input = panelLogin.querySelector("[data-cookie]");
    const msg = panelLogin.querySelector("[data-login-msg]");
    const btn = panelLogin.querySelector("[data-login]");
    btn.addEventListener("click", async () => {
      const cookie = input.value.trim();
      if (!cookie) { msg.textContent = "请粘贴 cookie"; return; }
      msg.textContent = "导入中…";
      btn.disabled = true;
      try {
        const r = await ctx.api.post("/login", { cookie });
        btn.disabled = false;
        if (r.error) { msg.textContent = r.error; } else { renderStatus(); }
      } catch (e) { btn.disabled = false; msg.textContent = "导入失败：" + String(e); }
    });
    panelLogin.querySelector("[data-back-qr]").addEventListener("click", (e) => { e.preventDefault(); renderLogin(); });
  };

  // renderLogin 未登录态：ptlogin2 二维码 + 轮询。
  const renderLogin = () => {
    stopPoll();
    panelLogin.innerHTML =
      '<div style="' + S.card + ';padding:16px">' +
      '<p style="font-size:13px;font-weight:600;color:var(--yy-text,#e8ecf4)">使用 QQ 扫码登录</p>' +
      '<p style="margin-top:6px;' + S.hint + '">打开手机 QQ 扫一扫，扫描下方二维码并在手机上确认登录</p>' +
      '<div style="margin-top:14px;display:flex;align-items:flex-start;gap:16px">' +
      '<div style="width:168px;height:168px;border-radius:12px;background:#fff;flex:none;display:flex;align-items:center;justify-content:center;overflow:hidden">' +
      '<img data-qr-img alt="QQ 登录二维码" style="width:100%;height:100%;object-fit:contain"></div>' +
      '<div style="flex:1;min-width:0">' +
      '<p data-qr-tip style="font-size:13px;color:var(--yy-text,#e8ecf4)">正在生成二维码…</p>' +
      '<button type="button" data-qr-refresh style="margin-top:12px;height:32px;border-radius:999px;padding:0 16px;font-size:13px;color:var(--yy-text,#e8ecf4);background:transparent;border:1px solid var(--yy-border,#2a3348);cursor:pointer">刷新二维码</button>' +
      '</div></div>' +
      '<p style="margin-top:12px;' + S.hint + '">无法扫码？<a href="#" data-switch-cookie style="color:var(--yy-glow,#c5d0e8)">改用 Cookie 导入</a></p></div>';
    const img = panelLogin.querySelector("[data-qr-img]");
    const tip = panelLogin.querySelector("[data-qr-tip]");
    let qrsig = "";
    // tick 每 2.5 秒询问扫码状态（66 待扫 / 67 已扫 / 65 过期 / 0 成功）。
    const tick = async () => {
      if (!qrsig) return;
      try {
        const r = await ctx.api.post("/qr-check", { qrsig });
        if (r.code === 0) {
          if (r.error) { tip.textContent = "登录回调失败：" + r.error + "（请点「刷新二维码」重试）"; stopPoll(); return; }
          tip.textContent = "登录成功！"; stopPoll(); renderStatus(); return;
        }
        if (r.error) { tip.textContent = r.error; }
        if (r.code === 65) { tip.textContent = "二维码已过期，请点「刷新二维码」"; stopPoll(); return; }
        if (r.code === 67) { tip.textContent = "已扫码，请在手机上点击「确认登录」"; }
        if (r.code === 66) { tip.textContent = "等待扫码…"; }
        pollTimer = setTimeout(tick, 2500);
      } catch (e) { tip.textContent = "状态查询失败：" + String(e); pollTimer = setTimeout(tick, 2500); }
    };
    const initQr = async () => {
      stopPoll();
      qrsig = "";
      img.removeAttribute("src");
      tip.textContent = "正在生成二维码…";
      try {
        const r = await ctx.api.post("/qr-init", {});
        if (r.error) { tip.textContent = "生成二维码失败：" + r.error; return; }
        qrsig = r.qrsig;
        img.src = r.qr_png;
        tip.textContent = "请使用手机 QQ 扫一扫";
        stopPoll();
        tick();
      } catch (e) { tip.textContent = "生成二维码失败：" + String(e); }
    };
    panelLogin.querySelector("[data-qr-refresh]").addEventListener("click", initQr);
    panelLogin.querySelector("[data-switch-cookie]").addEventListener("click", (e) => { e.preventDefault(); renderCookieLogin(); });
    initQr();
  };

  const renderStatus = async () => {
    try {
      const r = await ctx.api.get("/status");
      if (r.logged_in && r.uin) { renderLoggedIn(r.uin); renderPlaylists(); } else { renderLogin(); }
    } catch (e) { panelLogin.innerHTML = '<p style="font-size:13px;color:#ef4444">状态加载失败：' + escapeHtml(String(e)) + "</p>"; }
  };

  // ---------- Tab 2：我的歌单 ----------
  const loadSongs = async (tid, wrap) => {
    wrap.innerHTML = '<p style="' + S.hint + '">加载中…</p>';
    try {
      const r = await ctx.api.post("/playlist-songs", { tid: String(tid) });
      if (r.error) { wrap.innerHTML = '<p style="' + S.hint + '">' + escapeHtml(r.error) + "</p>"; wrap.dataset.loaded = "1"; return; }
      const songs = r.songs || [];
      wrap.innerHTML = "";
      for (const s of songs) {
        const row = document.createElement("div");
        row.style.cssText = "display:flex;align-items:center;gap:10px;padding:6px 0";
        const info = document.createElement("div");
        info.style.cssText = "min-width:0;flex:1";
        info.innerHTML = '<p style="font-size:13px;color:var(--yy-text,#e8ecf4);white-space:nowrap;overflow:hidden;text-overflow:ellipsis">' + escapeHtml(s.name) + " - " + escapeHtml(s.artist || "") + "</p>";
        const btn = document.createElement("button");
        btn.type = "button";
        btn.textContent = "▶";
        btn.style.cssText = "flex-shrink:0;height:28px;border-radius:999px;padding:0 12px;font-size:12px;color:var(--yy-text,#e8ecf4);background:transparent;border:1px solid " + GREEN + ";cursor:pointer";
        btn.addEventListener("click", () => preview.toggle(btn, null, () => ctx.api.post("/song-url", { songmid: s.song_mid })));
        row.appendChild(info);
        row.appendChild(btn);
        wrap.appendChild(row);
      }
      if (!songs.length) wrap.innerHTML = '<p style="' + S.hint + '">歌单为空</p>';
      wrap.dataset.loaded = "1";
    } catch (e) { wrap.innerHTML = '<p style="' + S.hint + '">加载失败：' + escapeHtml(String(e)) + "</p>"; wrap.dataset.loaded = "1"; }
  };

  const renderPlaylists = async () => {
    panelPlaylists.innerHTML = '<p style="' + S.hint + '">加载歌单中…</p>';
    try {
      const r = await ctx.api.get("/playlists");
      if (r.error) { panelPlaylists.innerHTML = '<p style="' + S.hint + '">' + escapeHtml(r.error) + "</p>"; return; }
      const list = r.playlists || [];
      if (!list.length) { panelPlaylists.innerHTML = '<p style="' + S.hint + '">暂无歌单，请先在「QQ音乐登录」扫码登录</p>'; return; }
      panelPlaylists.innerHTML = "";
      for (const p of list) {
        const card = document.createElement("div");
        card.style.cssText = "border-top:1px solid var(--yy-border,#2a3348);padding:10px 0";
        const head = document.createElement("div");
        head.style.cssText = "display:flex;align-items:center;gap:10px;cursor:pointer";
        const info = document.createElement("div");
        info.style.cssText = "min-width:0;flex:1";
        info.innerHTML = '<p style="font-size:13px;color:var(--yy-text,#e8ecf4);white-space:nowrap;overflow:hidden;text-overflow:ellipsis">' + escapeHtml(p.name) + '</p><p style="' + S.hint + '">' + p.song_cnt + " 首</p>";
        const setBtn = document.createElement("button");
        setBtn.type = "button";
        setBtn.textContent = "设为首页音乐";
        setBtn.style.cssText = "flex-shrink:0;height:28px;border-radius:999px;padding:0 12px;font-size:12px;color:var(--yy-text,#e8ecf4);background:transparent;border:1px solid var(--yy-border,#2a3348);cursor:pointer";
        setBtn.addEventListener("click", async (ev) => {
          ev.stopPropagation();
          const r2 = await ctx.api.post("/bgm-settings", { playlist_tid: String(p.tid), enabled: true });
          if (r2.error) { setBtn.textContent = "失败：" + r2.error; return; }
          setBtn.textContent = "✓ 已设为首页音乐";
          renderSettings();
        });
        const arrow = document.createElement("span");
        arrow.textContent = "▸";
        arrow.style.cssText = "color:var(--yy-text-2,#9aa6bc);flex-shrink:0";
        const wrap = document.createElement("div");
        wrap.style.cssText = "display:none;border-top:1px solid var(--yy-border,#2a3348);margin-top:8px;padding-top:6px";
        head.addEventListener("click", async () => {
          if (wrap.style.display === "none") {
            wrap.style.display = "block";
            arrow.textContent = "▾";
            if (!wrap.dataset.loaded) await loadSongs(p.tid, wrap);
          } else { wrap.style.display = "none"; arrow.textContent = "▸"; }
        });
        head.appendChild(info);
        head.appendChild(setBtn);
        head.appendChild(arrow);
        card.appendChild(head);
        card.appendChild(wrap);
        panelPlaylists.appendChild(card);
      }
    } catch (e) { panelPlaylists.innerHTML = '<p style="' + S.hint + '">歌单加载失败：' + escapeHtml(String(e)) + "</p>"; }
  };

  // ---------- Tab 3：设置 ----------
  const renderSettings = async () => {
    try {
      const s = await ctx.api.get("/bgm-settings");
      let name = "未选择";
      if (s.playlist_tid) {
        const pl = await ctx.api.get("/playlists");
        const hit = (pl.playlists || []).find((p) => String(p.tid) === String(s.playlist_tid));
        if (hit) name = hit.name;
      }
      panelSettings.innerHTML =
        '<div style="' + S.card + ';padding:16px;display:flex;align-items:center;justify-content:space-between">' +
        '<div><p style="font-size:13px;font-weight:600;color:var(--yy-text,#e8ecf4)">首页背景音乐</p>' +
        '<p style="margin-top:4px;' + S.hint + '">开启后首页右下角浮现悬浮播放器；当前歌单：' + escapeHtml(name) + '</p></div>' +
        '<button type="button" data-bgm-toggle style="height:32px;border-radius:999px;padding:0 16px;font-size:13px;font-weight:600;color:#fff;background:' + (s.enabled ? GREEN : "var(--yy-border,#2a3348)") + ';border:none;cursor:pointer">' + (s.enabled ? "已开启" : "已关闭") + "</button></div>";
      panelSettings.querySelector("[data-bgm-toggle]").addEventListener("click", async () => {
        const r = await ctx.api.post("/bgm-settings", { enabled: !s.enabled });
        if (r.error) { return; }
        renderSettings();
      });
    } catch (e) { panelSettings.innerHTML = '<p style="' + S.hint + '">设置加载失败：' + escapeHtml(String(e)) + "</p>"; }
  };

  // ---------- 初始化 ----------
  chrome.bindTabs(box);
  renderStatus();
  renderSettings();

  return () => { preview.stop(); stopPoll(); box.remove(); };
}

