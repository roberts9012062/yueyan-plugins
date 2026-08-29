// cmd/bilibili-video-plugin/frontend/player.js
// B站视频播放器内容块（blocks: type=bilibili）：
//   封面卡片点击播放 → 宿主公开桥接解析真实流地址（no-referrer 绕防盗链）→
//   <video> 播放（多段 durl 顺序衔接）+ 清晰度菜单（默认作者所选档位）+
//   「扫码登录 B 站」弹层（游客用自己的账号解锁 720P/1080P；token 存 localStorage）。
// ctx: { slot: "block:bilibili", el, api, user, props: {bvid,cid,title,cover,author,duration,quality,qualities} }
// 说明：播放/扫码走宿主公开桥接（匿名访客可用），不经 ctx.api（其需宿主登录）。
import { escapeHtml } from "/plugin-sdk/shared.js";
import { playDash } from "./dash-player.js?v=6"; // 版本参数：绕模块图缓存（升级必改）

// 公开桥接前缀与游客 token 存储键。
const BRIDGE = "/api/v1/video/bilibili";
const TOKEN_KEY = "yueyan-bilibili-guest-token";
const NAME_KEY = "yueyan-bilibili-guest-name";
const MODE_KEY = "yueyan-bilibili-player-mode";
const MODE_TTL = 30 * 60 * 1000; // 官方模式记忆有效期（ms）：过期后重新经 /url 校验设置，站长切回 custom 能自动恢复

// readMode 读取播放器模式记忆（会话级；TTL 内免 /url 直接官方嵌入，过期回退自研流程）。
function readMode() {
  try {
    const raw = JSON.parse(sessionStorage.getItem(MODE_KEY) || "null");
    if (raw && raw.mode === "official" && Date.now() - Number(raw.ts) < MODE_TTL) {
      return "official";
    }
  } catch (e) {
    /* 解析异常按无记忆处理 */
  }
  return "custom";
}

// bridgeCall 调用宿主公开桥接端点（POST JSON；返回解析后的 JSON）。
async function bridgeCall(path, body) {
  const res = await fetch(BRIDGE + path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body || {}),
  });
  return res.json();
}

// proxyStreamURL 把 B 站 CDN 地址包装为宿主同源流代理（纯函数）。
// B 站 CDN 对 Referer 严格校验（webview 直连可能注入页面 Referer 即 403），
// 经宿主代理（浏览器 UA + 无 Referer + Range 透传）稳定加载。
function proxyStreamURL(cdnURL) {
  return BRIDGE + "/stream?src=" + encodeURIComponent(cdnURL);
}

// proxyImageURL 把 B 站图床地址（封面/头像）包装为宿主同源图片代理（纯函数）。
function proxyImageURL(imgURL) {
  if (!imgURL) {
    return "";
  }
  return BRIDGE + "/image?src=" + encodeURIComponent(imgURL);
}

// formatDuration 秒 → mm:ss（纯函数）。
function formatDuration(sec) {
  const s = Math.max(0, Math.floor(Number(sec) || 0));
  const m = Math.floor(s / 60);
  return m + ":" + String(s % 60).padStart(2, "0");
}

export default function register(ctx) {
  const props = ctx.props || {};
  const bvid = props.bvid || "";
  const cid = Number(props.cid) || 0;
  const qualities = Array.isArray(props.qualities) ? props.qualities : [];

  // 补全标记：嵌入块缺清晰度表（接口发帖场景）时，首次 /url 响应附带表补全菜单
  let menuPending = qualities.length === 0;
  let selectedQn = Number(props.quality) || 32; // 用户选择的清晰度（菜单高亮）
  let currentQn = selectedQn; // 实际播放的清晰度（B 站按身份自动降级后的值）
  let guestToken = localStorage.getItem(TOKEN_KEY) || "";
  let guestName = localStorage.getItem(NAME_KEY) || "";
  let playing = false;
  let durlList = [];
  let dashGroup = null;
  let dashStop = null; // DASH 播放控制器（中断/清理）
  let segIndex = 0;
  let pendingPlayTip = ""; // MSE 装载完成（真正出图）后的提示条文案
  let pollTimer = null;

  const box = document.createElement("div");
  box.className = "bilibili-video-block";
  box.style.cssText = "margin:14px 0;border-radius:12px;overflow:hidden;border:1px solid var(--yy-border,#2a3348);background:var(--yy-card,#161c2b)";
  ctx.el.appendChild(box);

  // fmtDesc 清晰度展示名（qn → 文案；游客身份标注）。
  const fmtDesc = (qn) => {
    const q = qualities.find((it) => Number(it.qn) === qn);
    return q ? String(q.desc) : String(qn);
  };

  // needLoginQn 该档位是否需要登录 B 站（qualities 内标注）。
  const needLoginQn = (qn) => {
    const q = qualities.find((it) => Number(it.qn) === qn);
    return Boolean(q && q.need_login);
  };

  // renderOfficial B 站官方嵌入播放器（iframe）：浏览器直连 B 站 CDN，国内速度快；
  // 清晰度由 B 站播放器内选择（浏览器已登录 B 站则可用登录态看高清）。
  const renderOfficial = () => {
    playing = true;
    box.innerHTML =
      '<iframe src="https://player.bilibili.com/player.html?bvid=' + escapeHtml(bvid) +
      '&page=1&high_quality=1&danmaku=0&autoplay=0" scrolling="no" frameborder="no" framespacing="0" allowfullscreen="true" ' +
      'style="display:block;width:100%;aspect-ratio:16/9;border:none" title="B站视频"></iframe>';
  };

  // 官方嵌入模式（/url 下发 + 会话记忆）：直接 iframe，不走封面点击/解析流程。
  if (readMode() === "official") {
    renderOfficial();
    return () => {
      box.remove();
    };
  }

  // playQn 解析并播放指定清晰度。
  const playQn = async (qn) => {
    const stage = box.querySelector("[data-stage]");
    const tip = box.querySelector("[data-tip]");
    if (!stage || !cid) {
      return;
    }
    tip.textContent = "正在解析 " + fmtDesc(qn) + " 播放地址…";
    try {
      const r = await bridgeCall("/url", { bvid, cid, qn, guest_token: guestToken });
      if (r.error) {
        tip.textContent = r.error;
        return;
      }
      // 官方嵌入模式（全站设置下发）：记忆后换 iframe（本次解析结果弃用）
      if (r.player_mode === "official") {
        sessionStorage.setItem(MODE_KEY, JSON.stringify({ mode: "official", ts: Date.now() }));
        renderOfficial();
        return;
      }
      currentQn = Number(r.quality) || qn;
      durlList = Array.isArray(r.durl) ? r.durl : [];
      dashGroup = r.dash || null;
      segIndex = 0;
      // 菜单档位补全：老文章块 props 未存清晰度表（或存的是旧版低档表）时，
      // 以 /url 响应的全档位表合并补充（按 qn 去重，保留 need_login 标注）
      if (Array.isArray(r.qualities) && r.qualities.length > 0) {
        for (const q of r.qualities) {
          if (!qualities.some((it) => Number(it.qn) === Number(q.qn))) {
            qualities.push(q);
          }
        }
        menuPending = false;
      }
      renderMenu();
      renderVideo(qn);
      // 提示条报实际播放档位；与所选不一致时说明降级原因
      const srcNote = r.source === "guest" ? "（你的 B 站账号）" : r.source === "admin" ? "" : "（未登录 B 站）";
      const downgrade = currentQn !== qn ? "，已自动降级" : "";
      pendingPlayTip = "正在播放 " + String(r.quality_desc || fmtDesc(currentQn)) + downgrade + " " + srcNote;
      // DASH 走 MSE 渐进装载：装载期间提示「正在缓冲」，画面出图（playing）后切换文案；
      // durl 直连即点即播，直接展示最终文案
      if (!dashGroup) {
        tip.textContent = pendingPlayTip;
      }
      renderMenu();
    } catch (e) {
      tip.textContent = "解析失败：" + String(e);
    }
  };

  // renderVideo 渲染视频元素：DASH（1080P 高清仅有此形态）走 MSE 双流装载，
  // mp4 durl（720P 及以下兜底）直接 src 播放（多段顺序衔接）。
  const renderVideo = (targetQn) => {
    const stage = box.querySelector("[data-stage]");
    if (!stage || (durlList.length === 0 && !dashGroup)) {
      return;
    }
    stopPlayback();
    playing = true;
    stage.innerHTML = "";
    const video = document.createElement("video");
    video.controls = true;
    video.playsInline = true;
    video.preload = "auto";
    video.style.cssText = "display:block;width:100%;aspect-ratio:16/9;background:#000";
    if (dashGroup && durlList.length === 0) {
      // DASH（1080P 高清仅有此形态）：先挂载元素再装载（MediaSource 对 detached 元素行为不稳，实测必须先入 DOM）
      stage.appendChild(video);
      // 装载期间提示缓冲（低速中转链路起播需数十秒，避免"正在播放"误导黑屏等待）
      const bufferTip = box.querySelector("[data-tip]");
      if (bufferTip) {
        bufferTip.textContent = "正在缓冲 " + fmtDesc(targetQn) + "…（首次缓冲可能需要一点时间）";
      }
      video.addEventListener("playing", () => {
        const t = box.querySelector("[data-tip]");
        if (t) {
          t.textContent = pendingPlayTip;
        }
      }, { once: true });
      let degraded = false;
      const controller = playDash(video, dashGroup, targetQn, proxyStreamURL, (msg) => {
        const t = box.querySelector("[data-tip]");
        // 1080P MSE 解码失败时自动降级 720P（浏览器直连 durl，体验兜底不黑屏）
        if (!degraded && targetQn === 80) {
          degraded = true;
          if (t) {
            t.textContent = msg + "，自动切换 720P…";
          }
          selectedQn = 64;
          renderMenu();
          playQn(64);
          return;
        }
        if (t) {
          t.textContent = msg;
        }
      });
      dashStop = controller.stop;
      currentQn = controller.quality || currentQn;
      video.addEventListener("error", () => {
        const t = box.querySelector("[data-tip]");
        if (t) {
          t.textContent = "播放失败（流地址可能过期），请切换清晰度重试";
        }
      });
    } else {
      // mp4 durl：浏览器直连 B 站 CDN（no-referrer 绕防盗链；<video> 媒体元素
      // 不受 CORS 限制）——视频流量不经站长安服务器中转；直连被拦时回落同源代理
      const playSeg = (seg, viaProxy) => {
        video.referrerPolicy = "no-referrer";
        video.src = viaProxy ? proxyStreamURL(seg.url) : seg.url;
        video.play().catch(() => {}); // autoplay 被策略阻止时用户经原生控件播放
        return viaProxy;
      };
      let viaProxy = false;
      video.autoplay = true;
      playSeg(durlList[segIndex], false);
      video.addEventListener("ended", () => {
        segIndex++;
        if (segIndex < durlList.length) {
          playSeg(durlList[segIndex], viaProxy);
        }
      });
      video.addEventListener("error", () => {
        // 直连被 CDN/旧页面 CSP 拦截时一次性回落服务器代理重试（自愈，无需刷新）
        if (!viaProxy) {
          viaProxy = true;
          const t = box.querySelector("[data-tip]");
          if (t) {
            t.textContent = "直连受限，已切换服务器代理通道…";
          }
          playSeg(durlList[segIndex], true);
          return;
        }
        const t = box.querySelector("[data-tip]");
        if (t) {
          t.textContent = "播放失败（地址可能过期），请切换清晰度重试";
        }
      });
      stage.appendChild(video);
    }
  };

  // stopPlayback 停止当前播放（中断 DASH 装载流，重置状态）。
  const stopPlayback = () => {
    if (dashStop) {
      dashStop();
      dashStop = null;
    }
  };

  // renderMenu 渲染清晰度菜单与登录入口。
  const renderMenu = () => {
    const menu = box.querySelector("[data-menu]");
    if (!menu) {
      return;
    }
    const items = qualities
      .map((q) => {
        const qn = Number(q.qn);
        const active = qn === selectedQn; // 高亮跟随用户选择（实际档位由提示条说明）
        const locked = needLoginQn(qn) && !guestToken;
        const badge = locked ? ' <span style="font-size:10px;color:var(--yy-text-3,#6b7a95)">需登录</span>' : "";
        return (
          '<button type="button" data-qn="' + qn + '" style="height:26px;padding:0 10px;border-radius:999px;font-size:12px;cursor:pointer;border:1px solid ' +
          (active ? "#FB7299" : "var(--yy-border,#2a3348)") + ";color:" +
          (active ? "#FB7299" : "var(--yy-text-2,#9aa6bc)") + ';background:transparent">' +
          escapeHtml(String(q.desc)) + badge + "</button>"
        );
      })
      .join("");
    const loginBtn = guestToken
      ? '<span style="font-size:11px;color:var(--yy-text-2,#9aa6bc)">已登录 B 站：' + escapeHtml(guestName || "游客") + "</span>"
      : '<button type="button" data-bili-login style="height:26px;padding:0 12px;border-radius:999px;font-size:12px;color:#fff;background:#FB7299;border:none;cursor:pointer">扫码登录B站 · 解锁高清</button>';
    menu.innerHTML = '<div style="display:flex;align-items:center;gap:6px;flex-wrap:wrap">' + items + '<span style="flex:1"></span>' + loginBtn + "</div>";
    menu.querySelectorAll("[data-qn]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const qn = Number(btn.getAttribute("data-qn"));
        selectedQn = qn; // 高亮立即跟随选择，解析成功后提示条报实际档位
        renderMenu();
        if (qn !== currentQn || !playing) {
          playQn(qn);
        }
      });
    });
    const login = menu.querySelector("[data-bili-login]");
    if (login) {
      login.addEventListener("click", openQrOverlay);
    }
  };

  // openQrOverlay 游客扫码弹层（成功后存 token 并重播当前清晰度）。
  const openQrOverlay = async () => {
    const overlay = box.querySelector("[data-qr-overlay]");
    overlay.style.display = "flex";
    const qrBox = overlay.querySelector("[data-qr-img]");
    const status = overlay.querySelector("[data-qr-status]");
    qrBox.innerHTML = '<span style="font-size:12px;color:#6b7280">二维码加载中…</span>';
    status.textContent = "请用「哔哩哔哩」App 扫码";
    try {
      const init = await bridgeCall("/qr-init", {});
      if (init.error) {
        qrBox.innerHTML = '<span style="font-size:12px;color:#ef4444">' + escapeHtml(init.error) + "</span>";
        return;
      }
      qrBox.innerHTML = '<img src="' + init.qr_png + '" alt="二维码" style="width:180px;height:180px">';
      if (pollTimer) {
        clearInterval(pollTimer);
      }
      pollTimer = setInterval(async () => {
        try {
          const c = await bridgeCall("/guest-qr-check", {
            qrcode_key: init.qrcode_key,
            session_token: init.session_token,
          });
          if (c.code === 0 && c.guest_token) {
            clearInterval(pollTimer);
            pollTimer = null;
            guestToken = c.guest_token;
            guestName = c.nickname || "B站用户";
            localStorage.setItem(TOKEN_KEY, guestToken);
            localStorage.setItem(NAME_KEY, guestName);
            overlay.style.display = "none";
            playQn(selectedQn); // 用自己的账号重播所选档位（解锁高清）
          } else if (c.code === 86038) {
            clearInterval(pollTimer);
            pollTimer = null;
            status.textContent = "二维码已过期，请关闭后重试";
          } else if (c.code === 86090) {
            status.textContent = "已扫码，请在手机上确认";
          }
        } catch (e) {
          /* 轮询失败静默重试 */
        }
      }, 2500);
    } catch (e) {
      status.textContent = "二维码加载失败：" + String(e);
    }
  };

  // 初始渲染：封面卡片 + 信息栏 + 菜单 + 弹层骨架。
  box.innerHTML =
    '<div data-stage style="position:relative;cursor:pointer">' +
    '<img src="' + escapeHtml(proxyImageURL(props.cover)) + '" alt="" style="display:block;width:100%;aspect-ratio:16/9;object-fit:cover;background:#000">' +
    '<span style="position:absolute;right:8px;bottom:8px;padding:2px 8px;border-radius:6px;font-size:12px;color:#fff;background:rgba(0,0,0,.6)">' + escapeHtml(formatDuration(props.duration)) + "</span>" +
    '<span data-play-btn style="position:absolute;inset:0;display:flex;align-items:center;justify-content:center;font-size:44px;color:rgba(255,255,255,.92);background:rgba(0,0,0,.18);transition:background .2s">▶</span></div>' +
    '<div style="padding:12px 14px;display:flex;flex-direction:column;gap:10px">' +
    '<div><p style="font-size:14px;font-weight:600;color:var(--yy-text,#e8ecf4);line-height:1.5">' + escapeHtml(String(props.title || bvid)) + "</p>" +
    '<p style="margin-top:2px;font-size:12px;color:var(--yy-text-2,#9aa6bc)">UP：' + escapeHtml(String(props.author || "未知")) + " · 哔哩哔哩</p></div>" +
    '<div data-menu></div>' +
    '<p data-tip style="font-size:12px;color:var(--yy-text-3,#9aa6bc);min-height:16px"></p></div>' +
    '<div data-qr-overlay style="display:none;position:absolute;inset:0;z-index:5;align-items:center;justify-content:center;background:rgba(10,14,22,.92)">' +
    '<div style="display:flex;flex-direction:column;align-items:center;gap:10px;padding:16px;border-radius:12px;background:var(--yy-card,#161c2b);border:1px solid var(--yy-border,#2a3348)">' +
    '<div data-qr-img style="display:flex;align-items:center;justify-content:center;width:196px;height:196px;border-radius:8px;background:#fff"></div>' +
    '<p data-qr-status style="font-size:12px;color:var(--yy-text-2,#9aa6bc)">请用「哔哩哔哩」App 扫码</p>' +
    '<button type="button" data-qr-close style="height:30px;padding:0 14px;border-radius:999px;font-size:12px;color:var(--yy-text,#e8ecf4);background:transparent;border:1px solid var(--yy-border,#2a3348);cursor:pointer">关闭</button>' +
    "</div></div>";

  box.style.position = "relative"; // 弹层定位基准
  box.querySelector("[data-play-btn]").addEventListener("click", () => {
    if (!playing) {
      playQn(selectedQn);
    }
  });
  box.querySelector("[data-qr-close]").addEventListener("click", () => {
    if (pollTimer) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
    box.querySelector("[data-qr-overlay]").style.display = "none";
  });
  renderMenu();

  // 挂载时有 token 先校验有效性（失效静默清除，菜单回退「需登录」标注）。
  if (guestToken) {
    bridgeCall("/guest-status", { guest_token: guestToken }).then((r) => {
      if (!r || r.valid === false) {
        guestToken = "";
        guestName = "";
        localStorage.removeItem(TOKEN_KEY);
        localStorage.removeItem(NAME_KEY);
        renderMenu();
      }
    }).catch(() => {});
  }

  // 清理函数（停轮询与 DASH 装载；video 随 box 移除）。
  return () => {
    if (pollTimer) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
    stopPlayback();
    box.remove();
  };
}
