// cmd/netease-music-plugin/frontend/login-page.js
// 网易云音乐插件 · 后台登录页（admin.page /admin/plugin-pages/netease-music/login）：
//   网易云官方风格（主色 #C20C0C）：扫码登录（默认）+ 手机号登录双 Tab。
//   登录成功后持久化登录密钥（MUSIC_U / __csrf，插件 AES 加密落盘），
//   并提供「搜索试播」验证：拉取真实播放地址 → <audio> 播放（验证播放链路）。
// ctx: { container, api, user, params: {pluginId, page} }
// E2 去重：escapeHtml/试播/样式常量改用宿主共享 SDK（/plugin-sdk/shared.js，同源 ESM）。
import { escapeHtml, createAudioPreview, cardStyle, hintStyle } from "/plugin-sdk/shared.js";

export default function registerPage(ctx) {
  const RED = "#C20C0C";
  // 常用内联样式（input 为本页专用；card/hint 复用共享常量）
  const S = {
    input: "height:40px;border-radius:8px;border:1px solid var(--yy-border,#2a3348);padding:0 12px;font-size:13px;color:var(--yy-text,#e8ecf4)",
    card: cardStyle,
    hint: hintStyle,
  };
  const preview = createAudioPreview({ idle: "▶ 试播", loading: "加载中…", playing: "⏸ 播放中" });

  const box = document.createElement("div");
  box.style.cssText = "padding:24px;max-width:720px;margin:0 auto";
  ctx.container.appendChild(box);

  box.innerHTML =
    '<div style="display:flex;align-items:center;gap:12px">' +
    '<span style="display:inline-flex;align-items:center;justify-content:center;width:38px;height:38px;border-radius:50%;background:' + RED + ';color:#fff;font-size:19px;font-weight:700">♪</span>' +
    '<div><h1 style="font-size:18px;font-weight:700;color:var(--yy-text,#e8ecf4);line-height:1.3">网易云音乐</h1>' +
    '<p style="' + S.hint + '">登录网易云账号，获取歌曲真实播放地址</p></div></div>' +
    '<div style="margin-top:16px" data-zone></div>' +
    '<div style="margin-top:16px;' + S.card + ';padding:16px">' +
    '<p style="font-size:13px;font-weight:600;color:var(--yy-text,#e8ecf4)">搜索试播（验证播放链路）</p>' +
    '<div style="margin-top:10px;display:flex;gap:8px">' +
    '<input data-search-q type="text" placeholder="输入歌名，如：海阔天空" style="flex:1;' + S.input + '">' +
    '<button type="button" data-search-btn style="height:40px;border-radius:8px;padding:0 16px;font-size:13px;color:#fff;background:' + RED + ';border:none;cursor:pointer">搜索</button>' +
    '</div><div style="margin-top:10px" data-search-result></div></div>';

  const zone = box.querySelector("[data-zone]");
  const searchQ = box.querySelector("[data-search-q]");
  const searchBtn = box.querySelector("[data-search-btn]");
  const searchResult = box.querySelector("[data-search-result]");

  let qrTimer = null; // 扫码轮询定时器
  let smsTimer = null; // 验证码倒计时定时器

  // 搜索歌曲并渲染结果列表。
  const doSearch = async () => {
    const q = searchQ.value.trim();
    if (!q) {
      searchResult.textContent = "请输入歌名";
      return;
    }
    searchResult.textContent = "搜索中…";
    try {
      const r = await ctx.api.post("/search", { q, limit: 10 });
      if (r.error) {
        searchResult.textContent = r.error;
        return;
      }
      const songs = r.songs || [];
      searchResult.innerHTML = '<div style="' + S.hint + ';margin-bottom:6px" data-play-msg>' + (songs.length ? "点击「▶ 试播」验证播放地址" : "无结果") + "</div>";
      for (const s of songs) {
        const row = document.createElement("div");
        row.style.cssText = "display:flex;align-items:center;gap:12px;border-top:1px solid var(--yy-border,#2a3348);padding:10px 0";
        const info = document.createElement("div");
        info.style.cssText = "min-width:0;flex:1";
        info.innerHTML =
          '<p style="font-size:13px;color:var(--yy-text,#e8ecf4);white-space:nowrap;overflow:hidden;text-overflow:ellipsis">' +
          escapeHtml(s.name) + " - " + escapeHtml(s.artist || "") + "</p>" +
          '<p style="' + S.hint + ';white-space:nowrap;overflow:hidden;text-overflow:ellipsis">' + escapeHtml(s.album || "") + "</p>";
        const btn = document.createElement("button");
        btn.type = "button";
        btn.textContent = "▶ 试播";
        btn.style.cssText = "flex-shrink:0;height:30px;border-radius:999px;padding:0 14px;font-size:12px;color:var(--yy-text,#e8ecf4);background:transparent;border:1px solid " + RED + ";cursor:pointer";
        btn.addEventListener("click", () => preview.toggle(btn, searchResult.querySelector("[data-play-msg]"), () => ctx.api.post("/song-url", { id: s.id })));
        row.appendChild(info);
        row.appendChild(btn);
        searchResult.appendChild(row);
      }
    } catch (e) {
      searchResult.textContent = "搜索失败：" + String(e);
    }
  };

  // 已登录态：账号卡片 + 登出。
  const renderLoggedIn = (profile) => {
    preview.stop();
    zone.innerHTML =
      '<div style="' + S.card + ';padding:16px;display:flex;align-items:center;justify-content:space-between">' +
      '<div style="display:flex;align-items:center;gap:12px">' +
      (profile.avatar_url
        ? '<img src="' + escapeHtml(profile.avatar_url) + '" alt="" referrerpolicy="no-referrer" style="width:44px;height:44px;border-radius:50%">'
        : '<span style="display:inline-flex;align-items:center;justify-content:center;width:44px;height:44px;border-radius:50%;background:' + RED + ';color:#fff;font-weight:700">♪</span>') +
      '<div><p style="font-size:14px;font-weight:600;color:var(--yy-text,#e8ecf4)">已登录：' + escapeHtml(profile.nickname || "网易云用户") + "</p>" +
      '<p style="' + S.hint + '">登录密钥已保存，重启不丢失；访客可播放站内音乐</p></div></div>' +
      '<button type="button" data-logout style="height:32px;border-radius:999px;padding:0 16px;font-size:13px;color:var(--yy-text,#e8ecf4);background:transparent;border:1px solid var(--yy-border,#2a3348);cursor:pointer">登出</button>' +
      "</div>";
    zone.querySelector("[data-logout]").addEventListener("click", async () => {
      await ctx.api.post("/logout", {});
      renderStatus();
    });
  };

  // 扫码登录面板：二维码 + 轮询（801 待扫 / 802 已扫待确认 / 803 成功 / 800 过期）。
  const renderQr = (panel) => {
    if (qrTimer) {
      clearInterval(qrTimer);
      qrTimer = null;
    }
    panel.innerHTML =
      '<div style="display:flex;flex-direction:column;align-items:center;padding:8px 0 4px">' +
      '<div data-qr-box style="display:flex;align-items:center;justify-content:center;width:220px;height:220px;border-radius:8px;border:1px solid var(--yy-border,#2a3348);background:#fff">' +
      '<span style="' + S.hint + '">二维码加载中…</span></div>' +
      '<p data-qr-status style="margin-top:12px;font-size:13px;color:var(--yy-text-2,#9aa6bc)">请用「网易云音乐」App 扫码登录</p>' +
      '<button type="button" data-qr-refresh style="margin-top:8px;height:30px;border-radius:999px;padding:0 16px;font-size:12px;color:var(--yy-text,#e8ecf4);background:transparent;border:1px solid ' + RED + ';cursor:pointer">刷新二维码</button>' +
      "</div>";

    const qrBox = panel.querySelector("[data-qr-box]");
    const qrStatus = panel.querySelector("[data-qr-status]");

    const loadQr = async () => {
      try {
        const r = await ctx.api.post("/qr-unikey", {});
        if (r.error) {
          qrBox.innerHTML = '<span style="font-size:12px;color:#ef4444">' + escapeHtml(r.error) + "</span>";
          return;
        }
        qrBox.innerHTML = '<img src="' + r.qr_png + '" alt="二维码" style="width:200px;height:200px">';
        qrStatus.textContent = "请用「网易云音乐」App 扫码（微信/支付宝扫码无效）";
        qrTimer = setInterval(async () => {
          try {
            const c = await ctx.api.post("/qr-check", { unikey: r.unikey });
            if (c.code === 803) {
              clearInterval(qrTimer);
              qrTimer = null;
              renderStatus();
            } else if (c.code === 800) {
              clearInterval(qrTimer);
              qrTimer = null;
              qrStatus.textContent = "二维码已过期，请点击刷新";
            } else if (c.code === 802) {
              qrStatus.textContent = "已扫码，请在手机上确认";
            } else if (c.code === 801) {
              qrStatus.textContent = "等待网易云 App 扫码…";
            }
          } catch (e) {
            /* 轮询失败静默，下轮重试 */
          }
        }, 2500);
      } catch (e) {
        qrStatus.textContent = "二维码加载失败：" + String(e);
      }
    };
    panel.querySelector("[data-qr-refresh]").addEventListener("click", loadQr);
    loadQr();
  };

  // 手机号登录面板（密码 / 验证码两种模式，均走 eapi 登录）。
  const renderPwd = (panel) => {
    if (qrTimer) {
      clearInterval(qrTimer);
      qrTimer = null;
    }
    panel.innerHTML =
      '<div style="display:flex;flex-direction:column;gap:12px;padding:8px 0 4px;max-width:320px;margin:0 auto">' +
      '<div style="display:flex;gap:8px">' +
      '<span style="display:inline-flex;align-items:center;height:40px;border-radius:8px;border:1px solid var(--yy-border,#2a3348);padding:0 12px;font-size:13px;color:var(--yy-text-2,#9aa6bc);background:var(--yy-muted,#f3f4f6)">+86</span>' +
      '<input data-phone type="text" placeholder="手机号" style="flex:1;' + S.input + '"></div>' +
      '<input data-password type="password" placeholder="密码" style="' + S.input + '">' +
      '<div data-captcha-row style="display:none;gap:8px">' +
      '<input data-captcha type="text" placeholder="短信验证码" style="flex:1;' + S.input + '">' +
      '<button type="button" data-sms-btn style="flex-shrink:0;height:40px;border-radius:8px;padding:0 12px;font-size:12px;color:var(--yy-text,#e8ecf4);background:transparent;border:1px solid ' + RED + ';cursor:pointer">获取验证码</button></div>' +
      '<div style="display:flex;justify-content:flex-end">' +
      '<button type="button" data-mode-toggle style="height:28px;padding:0 8px;font-size:12px;color:var(--yy-text-2,#9aa6bc);background:transparent;border:none;cursor:pointer">验证码登录</button></div>' +
      '<button type="button" data-login style="height:40px;border-radius:8px;font-size:14px;font-weight:600;color:#fff;background:' + RED + ';border:none;cursor:pointer">登录</button>' +
      '<span data-login-msg style="' + S.hint + ';min-height:16px"></span></div>';

    const phone = panel.querySelector("[data-phone]");
    const password = panel.querySelector("[data-password]");
    const captchaRow = panel.querySelector("[data-captcha-row]");
    const captchaInput = panel.querySelector("[data-captcha]");
    const smsBtn = panel.querySelector("[data-sms-btn]");
    const modeToggle = panel.querySelector("[data-mode-toggle]");
    const msg = panel.querySelector("[data-login-msg]");
    const loginBtn = panel.querySelector("[data-login]");

    let captchaMode = false;

    // setMode 切换密码 / 验证码登录模式。
    const setMode = (useCaptcha) => {
      captchaMode = useCaptcha;
      password.style.display = useCaptcha ? "none" : "";
      captchaRow.style.display = useCaptcha ? "flex" : "none";
      modeToggle.textContent = useCaptcha ? "密码登录" : "验证码登录";
      msg.textContent = "";
    };

    // sendSMS 发送验证码并 60 秒倒计时。
    const sendSMS = async () => {
      if (!phone.value) {
        msg.textContent = "请先输入手机号";
        return;
      }
      smsBtn.disabled = true;
      msg.textContent = "验证码发送中…";
      try {
        const r = await ctx.api.post("/sms-send", { phone: phone.value });
        if (r.error) {
          msg.textContent = r.error;
          smsBtn.disabled = false;
          return;
        }
        msg.textContent = "验证码已发送，请查收短信";
        let count = 60;
        smsBtn.textContent = count + "s";
        smsTimer = setInterval(() => {
          count--;
          if (count <= 0) {
            clearInterval(smsTimer);
            smsTimer = null;
            smsBtn.textContent = "获取验证码";
            smsBtn.disabled = false;
          } else {
            smsBtn.textContent = count + "s";
          }
        }, 1000);
      } catch (e) {
        smsBtn.disabled = false;
        msg.textContent = "发送失败：" + String(e);
      }
    };

    // doLogin 登录（按当前模式提交密码或验证码）。
    const doLogin = async () => {
      if (!phone.value) {
        msg.textContent = "请输入手机号";
        return;
      }
      if (captchaMode && !captchaInput.value) {
        msg.textContent = "请输入短信验证码";
        return;
      }
      if (!captchaMode && !password.value) {
        msg.textContent = "请输入密码";
        return;
      }
      msg.textContent = "登录中…";
      loginBtn.disabled = true;
      const body = captchaMode
        ? { phone: phone.value, captcha: captchaInput.value }
        : { phone: phone.value, password: password.value };
      try {
        const r = await ctx.api.post("/login", body);
        loginBtn.disabled = false;
        if (r.error) {
          msg.textContent = r.error;
        } else {
          renderStatus();
        }
      } catch (e) {
        loginBtn.disabled = false;
        msg.textContent = "登录失败：" + String(e);
      }
    };

    loginBtn.addEventListener("click", doLogin);
    modeToggle.addEventListener("click", () => setMode(!captchaMode));
    smsBtn.addEventListener("click", sendSMS);
    password.addEventListener("keydown", (e) => {
      if (e.key === "Enter") doLogin();
    });
    captchaInput.addEventListener("keydown", (e) => {
      if (e.key === "Enter") doLogin();
    });
  };

  // 未登录态：网易云风格登录卡片（扫码 / 手机号 Tab）。
  const renderLogin = () => {
    zone.innerHTML =
      '<div style="' + S.card + ';overflow:hidden">' +
      '<div style="display:flex;border-bottom:1px solid var(--yy-border,#2a3348)">' +
      '<button type="button" data-tab-qr style="flex:1;height:44px;font-size:14px;font-weight:600;border:none;cursor:pointer;background:transparent;color:var(--yy-glow,#c5d0e8);border-bottom:2px solid ' + RED + '">扫码登录</button>' +
      '<button type="button" data-tab-pwd style="flex:1;height:44px;font-size:14px;border:none;cursor:pointer;background:transparent;color:var(--yy-text-2,#9aa6bc);border-bottom:2px solid transparent">手机号登录</button>' +
      '</div><div style="padding:20px" data-login-panel></div></div>';

    const tabQr = zone.querySelector("[data-tab-qr]");
    const tabPwd = zone.querySelector("[data-tab-pwd]");
    const panel = zone.querySelector("[data-login-panel]");
    const setTab = (active) => {
      const qrActive = active === "qr";
      tabQr.style.color = qrActive ? "var(--yy-glow,#c5d0e8)" : "var(--yy-text-2,#9aa6bc)";
      tabQr.style.borderBottom = qrActive ? "2px solid " + RED : "2px solid transparent";
      tabPwd.style.color = qrActive ? "var(--yy-text-2,#9aa6bc)" : "var(--yy-glow,#c5d0e8)";
      tabPwd.style.borderBottom = qrActive ? "2px solid transparent" : "2px solid " + RED;
    };
    tabQr.addEventListener("click", () => {
      setTab("qr");
      renderQr(panel);
    });
    tabPwd.addEventListener("click", () => {
      setTab("pwd");
      renderPwd(panel);
    });
    renderQr(panel); // 默认扫码登录（网易云官网默认）
  };

  // 查询登录态并分流渲染。
  const renderStatus = async () => {
    try {
      const r = await ctx.api.get("/status");
      if (r.logged_in && r.profile) {
        renderLoggedIn(r.profile);
      } else {
        renderLogin();
      }
    } catch (e) {
      zone.innerHTML = '<p style="font-size:13px;color:#ef4444">状态加载失败：' + escapeHtml(String(e)) + "</p>";
    }
  };

  searchBtn.addEventListener("click", doSearch);
  searchQ.addEventListener("keydown", (e) => {
    if (e.key === "Enter") doSearch();
  });
  renderStatus();

  // 清理函数（页面卸载时停止轮询、倒计时与播放）。
  return () => {
    if (qrTimer) {
      clearInterval(qrTimer);
      qrTimer = null;
    }
    if (smsTimer) {
      clearInterval(smsTimer);
      smsTimer = null;
    }
    preview.stop();
    box.remove();
  };
}
