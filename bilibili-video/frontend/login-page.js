// cmd/bilibili-video-plugin/frontend/login-page.js
// B站视频插件 · 后台登录页（/admin/plugin-pages/bilibili-video/login）：
//   B站粉主题（#FB7299）：扫码登录（默认）+ 手机验证码登录双 Tab + Cookie 导入备用。
//   登录成功后登录密钥（SESSDATA 等）由插件 AES 加密落盘，重启不丢失；
//   站长登录后发帖可解锁 720P/1080P 高清档位，游客可观看高清。
// ctx: { container, api, user, params: {pluginId, page} }
import { escapeHtml, cardStyle, hintStyle } from "/plugin-sdk/shared.js";

// BRIDGE 宿主公开桥接前缀（图片代理用；登录页 API 仍走 ctx.api）。
const BRIDGE = "/api/v1/video/bilibili";

// proxyImageURL B 站图床地址（头像）经宿主同源代理加载（纯函数；防盗链）。
function proxyImageURL(imgURL) {
  if (!imgURL) {
    return "";
  }
  return BRIDGE + "/image?src=" + encodeURIComponent(imgURL);
}

export default function registerPage(ctx) {
  const PINK = "#FB7299";
  // 常用内联样式（input 本页专用；card/hint 复用宿主共享常量）
  const S = {
    input: "height:40px;border-radius:8px;border:1px solid var(--yy-border,#2a3348);padding:0 12px;font-size:13px;color:var(--yy-text,#e8ecf4);background:transparent",
    card: cardStyle,
    hint: hintStyle,
    btn: "height:40px;border-radius:8px;padding:0 16px;font-size:13px;color:#fff;background:" + PINK + ";border:none;cursor:pointer",
  };

  const box = document.createElement("div");
  box.style.cssText = "padding:24px;max-width:720px;margin:0 auto";
  ctx.container.appendChild(box);

  box.innerHTML =
    '<div style="display:flex;align-items:center;gap:12px">' +
    '<span style="display:inline-flex;align-items:center;justify-content:center;width:38px;height:38px;border-radius:50%;background:' + PINK + ';color:#fff;font-size:17px;font-weight:700">B</span>' +
    '<div><h1 style="font-size:18px;font-weight:700;color:var(--yy-text,#e8ecf4);line-height:1.3">B站视频</h1>' +
    '<p style="' + S.hint + '">登录 B 站账号后，发帖可插入 1080P 高清视频，游客可观看</p></div></div>' +
    '<div style="margin-top:16px" data-zone></div>' +
    '<div style="margin-top:16px;' + S.card + ';padding:14px 16px">' +
    '<p style="font-size:12px;line-height:1.7;color:var(--yy-text-2,#9aa6bc)">说明：登录密钥仅用于向 B 站请求视频播放地址；' +
    "设置中的「允许游客用站长账号看高清」关闭后，未登录 B 站的访客最高观看 480P；" +
    "访客也可在帖子播放器上扫码登录自己的 B 站账号观看高清。</p></div>";

  const zone = box.querySelector("[data-zone]");

  let qrTimer = null; // 扫码轮询定时器
  let smsTimer = null; // 验证码倒计时定时器

  // 已登录态：账号卡片（昵称/等级/大会员徽章）+ 登出。
  const renderLoggedIn = (profile) => {
    zone.innerHTML =
      '<div style="' + S.card + ';padding:16px;display:flex;align-items:center;justify-content:space-between">' +
      '<div style="display:flex;align-items:center;gap:12px">' +
      (profile.avatar
        ? '<img src="' + escapeHtml(proxyImageURL(profile.avatar)) + '" alt="" style="width:44px;height:44px;border-radius:50%">'
        : '<span style="display:inline-flex;align-items:center;justify-content:center;width:44px;height:44px;border-radius:50%;background:' + PINK + ';color:#fff;font-weight:700">B</span>') +
      '<div><p style="font-size:14px;font-weight:600;color:var(--yy-text,#e8ecf4)">' + escapeHtml(profile.nickname || "B站用户") +
      (profile.vip ? ' <span style="display:inline-block;margin-left:6px;padding:1px 8px;border-radius:999px;font-size:11px;color:#b45309;background:#fde68a">大会员</span>' : "") + "</p>" +
      '<p style="' + S.hint + '">Lv.' + Number(profile.level || 0) + " · 登录密钥已加密保存，发帖可选 1080P</p></div></div>" +
      '<button type="button" data-logout style="height:32px;border-radius:999px;padding:0 16px;font-size:13px;color:var(--yy-text,#e8ecf4);background:transparent;border:1px solid var(--yy-border,#2a3348);cursor:pointer">登出</button>' +
      "</div>";
    zone.querySelector("[data-logout]").addEventListener("click", async () => {
      await ctx.api.post("/logout", {});
      renderStatus();
    });
  };

  // 扫码登录面板：二维码 + 轮询（86101 待扫 / 86090 已扫待确认 / 0 成功 / 86038 过期）。
  const renderQr = (panel) => {
    if (qrTimer) {
      clearInterval(qrTimer);
      qrTimer = null;
    }
    panel.innerHTML =
      '<div style="display:flex;flex-direction:column;align-items:center;padding:8px 0 4px">' +
      '<div data-qr-box style="display:flex;align-items:center;justify-content:center;width:220px;height:220px;border-radius:8px;border:1px solid var(--yy-border,#2a3348);background:#fff">' +
      '<span style="font-size:12px;color:#6b7280">二维码加载中…</span></div>' +
      '<p data-qr-status style="margin-top:12px;font-size:13px;color:var(--yy-text-2,#9aa6bc)">请用「哔哩哔哩」App 扫码登录</p>' +
      '<button type="button" data-qr-refresh style="margin-top:8px;height:30px;border-radius:999px;padding:0 16px;font-size:12px;color:var(--yy-text,#e8ecf4);background:transparent;border:1px solid ' + PINK + ';cursor:pointer">刷新二维码</button>' +
      "</div>";

    const qrBox = panel.querySelector("[data-qr-box]");
    const qrStatus = panel.querySelector("[data-qr-status]");

    // loadQr 拉取二维码并启动轮询。
    const loadQr = async () => {
      try {
        const r = await ctx.api.post("/qr-init", {});
        if (r.error) {
          qrBox.innerHTML = '<span style="font-size:12px;color:#ef4444">' + escapeHtml(r.error) + "</span>";
          return;
        }
        qrBox.innerHTML = '<img src="' + r.qr_png + '" alt="二维码" style="width:200px;height:200px">';
        qrStatus.textContent = "请用「哔哩哔哩」App 扫码（微信/支付宝扫码无效）";
        qrTimer = setInterval(async () => {
          try {
            const c = await ctx.api.post("/qr-check", { qrcode_key: r.qrcode_key, session_token: r.session_token });
            if (c.code === 0) {
              clearInterval(qrTimer);
              qrTimer = null;
              if (c.error) {
                qrStatus.textContent = c.error; // 保存失败等错误如实提示（不静默回扫码界面）
                return;
              }
              renderStatus();
            } else if (c.code === 86038) {
              clearInterval(qrTimer);
              qrTimer = null;
              qrStatus.textContent = "二维码已过期，请点击刷新";
            } else if (c.code === 86090) {
              qrStatus.textContent = "已扫码，请在手机上确认";
            } else if (c.code === 86101) {
              qrStatus.textContent = "等待哔哩哔哩 App 扫码…";
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

  // 手机验证码面板：手机号 + 验证码（60 秒倒计时；B 站风控时提示改扫码）。
  const renderSms = (panel) => {
    if (qrTimer) {
      clearInterval(qrTimer);
      qrTimer = null;
    }
    panel.innerHTML =
      '<div style="display:flex;flex-direction:column;gap:12px;padding:8px 0 4px;max-width:320px;margin:0 auto">' +
      '<div style="display:flex;gap:8px">' +
      '<span style="display:inline-flex;align-items:center;height:40px;border-radius:8px;border:1px solid var(--yy-border,#2a3348);padding:0 12px;font-size:13px;color:var(--yy-text-2,#9aa6bc)">+86</span>' +
      '<input data-tel type="text" placeholder="手机号" style="flex:1;' + S.input + '"></div>' +
      '<div style="display:flex;gap:8px">' +
      '<input data-code type="text" placeholder="短信验证码" style="flex:1;' + S.input + '">' +
      '<button type="button" data-sms-btn style="flex-shrink:0;height:40px;border-radius:8px;padding:0 12px;font-size:12px;color:var(--yy-text,#e8ecf4);background:transparent;border:1px solid ' + PINK + ';cursor:pointer">获取验证码</button></div>' +
      '<button type="button" data-login style="' + S.btn + '">登录</button>' +
      '<span data-login-msg style="' + S.hint + ';min-height:16px"></span>' +
      '<details style="margin-top:4px"><summary style="font-size:12px;color:var(--yy-text-2,#9aa6bc);cursor:pointer;outline:none">Cookie 导入（备用）</summary>' +
      '<textarea data-cookie rows="3" placeholder="粘贴浏览器 F12 复制的 Cookie（需含 SESSDATA=…）" style="margin-top:8px;width:100%;box-sizing:border-box;border-radius:8px;border:1px solid var(--yy-border,#2a3348);padding:8px 10px;font-size:12px;color:var(--yy-text,#e8ecf4);background:transparent;resize:vertical"></textarea>' +
      '<button type="button" data-cookie-btn style="margin-top:8px;height:34px;border-radius:8px;padding:0 14px;font-size:12px;color:#fff;background:' + PINK + ';border:none;cursor:pointer">导入 Cookie</button></details></div>';

    const tel = panel.querySelector("[data-tel]");
    const code = panel.querySelector("[data-code]");
    const smsBtn = panel.querySelector("[data-sms-btn]");
    const msg = panel.querySelector("[data-login-msg]");
    const loginBtn = panel.querySelector("[data-login]");
    const cookieBtn = panel.querySelector("[data-cookie-btn]");
    const cookieArea = panel.querySelector("[data-cookie]");

    // sendSMS 发送验证码（60 秒倒计时）。
    const sendSMS = async () => {
      if (!tel.value.trim()) {
        msg.textContent = "请先输入手机号";
        return;
      }
      smsBtn.disabled = true;
      msg.textContent = "验证码发送中…";
      try {
        const r = await ctx.api.post("/sms-send", { tel: tel.value.trim(), country_id: 86 });
        if (!r.ok) {
          msg.textContent = r.message || r.error || "发送失败";
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

    // doLogin 验证码登录。
    const doLogin = async () => {
      if (!tel.value.trim() || !code.value.trim()) {
        msg.textContent = "请输入手机号与验证码";
        return;
      }
      msg.textContent = "登录中…";
      loginBtn.disabled = true;
      try {
        const r = await ctx.api.post("/sms-login", { tel: tel.value.trim(), code: code.value.trim(), country_id: 86 });
        loginBtn.disabled = false;
        if (r.error) {
          msg.textContent = r.message || r.error;
        } else {
          renderStatus();
        }
      } catch (e) {
        loginBtn.disabled = false;
        msg.textContent = "登录失败：" + String(e);
      }
    };

    // doCookieLogin Cookie 导入。
    const doCookieLogin = async () => {
      if (!cookieArea.value.trim()) {
        msg.textContent = "请粘贴 Cookie";
        return;
      }
      const r = await ctx.api.post("/cookie-login", { cookie: cookieArea.value.trim() });
      if (r.error) {
        msg.textContent = r.error;
      } else {
        renderStatus();
      }
    };

    loginBtn.addEventListener("click", doLogin);
    smsBtn.addEventListener("click", sendSMS);
    cookieBtn.addEventListener("click", doCookieLogin);
    code.addEventListener("keydown", (e) => {
      if (e.key === "Enter") doLogin();
    });
  };

  // 未登录态：登录卡片（扫码 / 手机号 Tab）。
  const renderLogin = () => {
    zone.innerHTML =
      '<div style="' + S.card + ';overflow:hidden">' +
      '<div style="display:flex;border-bottom:1px solid var(--yy-border,#2a3348)">' +
      '<button type="button" data-tab-qr style="flex:1;height:44px;font-size:14px;font-weight:600;border:none;cursor:pointer;background:transparent;color:var(--yy-glow,#c5d0e8);border-bottom:2px solid ' + PINK + '">扫码登录</button>' +
      '<button type="button" data-tab-sms style="flex:1;height:44px;font-size:14px;border:none;cursor:pointer;background:transparent;color:var(--yy-text-2,#9aa6bc);border-bottom:2px solid transparent">手机号登录</button>' +
      '</div><div style="padding:20px" data-login-panel></div></div>';

    const tabQr = zone.querySelector("[data-tab-qr]");
    const tabSms = zone.querySelector("[data-tab-sms]");
    const panel = zone.querySelector("[data-login-panel]");
    // setTab 切换 Tab 高亮。
    const setTab = (active) => {
      const qrActive = active === "qr";
      tabQr.style.color = qrActive ? "var(--yy-glow,#c5d0e8)" : "var(--yy-text-2,#9aa6bc)";
      tabQr.style.borderBottom = qrActive ? "2px solid " + PINK : "2px solid transparent";
      tabSms.style.color = qrActive ? "var(--yy-text-2,#9aa6bc)" : "var(--yy-glow,#c5d0e8)";
      tabSms.style.borderBottom = qrActive ? "2px solid transparent" : "2px solid " + PINK;
    };
    tabQr.addEventListener("click", () => {
      setTab("qr");
      renderQr(panel);
    });
    tabSms.addEventListener("click", () => {
      setTab("sms");
      renderSms(panel);
    });
    renderQr(panel); // 默认扫码登录（最稳通道）
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

  renderStatus();

  // 清理函数（页面卸载时停止轮询与倒计时）。
  return () => {
    if (qrTimer) {
      clearInterval(qrTimer);
      qrTimer = null;
    }
    if (smsTimer) {
      clearInterval(smsTimer);
      smsTimer = null;
    }
    box.remove();
  };
}
