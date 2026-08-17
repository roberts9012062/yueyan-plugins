// cmd/netease-music-plugin/frontend/settings-page.js
// 网易云音乐插件 · 后台设置页（admin.page /admin/plugin-pages/netease-music/settings）：
//   1. 站长登录（手机号+密码，经插件 API /login 走 eapi）
//   2. 登录状态展示 + 登出
//   3. 搜索歌曲 → 点击试播（经 /song-url 拿真实地址 → <audio> 播放）
// ctx: { container, api, user, params: {pluginId, page} }
import { createAudioPreview } from "/plugin-sdk/shared.js";

export default function registerPage(ctx) {
  const box = document.createElement("div");
  box.className = "netease-music-settings";
  box.style.padding = "24px";
  box.style.maxWidth = "720px";
  ctx.container.appendChild(box);

  box.innerHTML =
    '<h1 class="text-xl font-semibold" style="color:var(--yy-text,#e8ecf4)">网易云音乐插件</h1>' +
    '<p class="text-sm" style="color:var(--yy-text-2,#9aa6bc)">站长登录网易云后，插件可获取歌曲真实播放地址，访客无需登录即可播放。</p>' +
    // 登录状态区
    '<div class="mt-4 rounded-lg border p-4" style="border-color:var(--yy-border,#2a3348);background:var(--yy-elevated,#fff)" data-status-zone></div>' +
    // 搜索试播区
    '<div class="mt-4 rounded-lg border p-4" style="border-color:var(--yy-border,#2a3348);background:var(--yy-elevated,#fff)">' +
    '<p class="text-sm font-medium" style="color:var(--yy-text,#e8ecf4)">搜索试播（验证播放链路）</p>' +
    '<div class="mt-2 flex gap-2">' +
    '<input data-search-q type="text" placeholder="输入歌名，如：海阔天空" class="h-9 flex-1 rounded-lg border px-3 text-sm" style="border-color:var(--yy-border,#2a3348);color:var(--yy-text,#e8ecf4)">' +
    '<button type="button" data-search-btn class="rounded-lg px-4 text-sm text-white" style="background:var(--yy-accent,#6366f1)">搜索</button>' +
    "</div>" +
    '<div class="mt-3" data-search-result></div>' +
    "</div>";

  const statusZone = box.querySelector("[data-status-zone]");
  const searchQ = box.querySelector("[data-search-q]");
  const searchBtn = box.querySelector("[data-search-btn]");
  const searchResult = box.querySelector("[data-search-result]");

  // 播放器（试播用，共享控制器——E2 去重）
  const preview = createAudioPreview({ idle: "▶ 试播", loading: "加载中…", playing: "⏸ 播放中" });

  // 渲染登录状态区
  const renderStatus = async () => {
    try {
      const r = await ctx.api.get("/status");
      if (r.logged_in && r.profile) {
        statusZone.innerHTML =
          '<div class="flex items-center justify-between">' +
          '<div class="flex items-center gap-3">' +
          (r.profile.avatar_url
            ? '<img src="' + r.profile.avatar_url + '" alt="" class="h-10 w-10 rounded-full" referrerpolicy="no-referrer">'
            : "") +
          '<div><p class="text-sm font-medium" style="color:var(--yy-text,#e8ecf4)">已登录：' + r.profile.nickname + "</p>" +
          '<p class="text-xs" style="color:var(--yy-text-2,#9aa6bc)">网易云账号（访客可直接播放站内音乐）</p></div>' +
          "</div>" +
          '<button type="button" data-logout class="rounded-full border px-4 py-1.5 text-sm" style="border-color:var(--yy-border,#2a3348);color:var(--yy-text,#e8ecf4)">登出</button>' +
          "</div>";
        box.querySelector("[data-logout]").addEventListener("click", async () => {
          await ctx.api.post("/logout", {});
          renderStatus();
        });
      } else {
        statusZone.innerHTML =
          '<p class="text-sm font-medium" style="color:var(--yy-text,#e8ecf4)">未登录</p>' +
          '<div class="mt-3 flex gap-2">' +
          '<button type="button" data-tab-pwd class="rounded-full px-4 py-1 text-xs">密码登录</button>' +
          '<button type="button" data-tab-qr class="rounded-full px-4 py-1 text-xs">扫码登录</button>' +
          "</div>" +
          '<div class="mt-3 max-w-sm" data-login-panel></div>';
        const panel = box.querySelector("[data-login-panel]");
        const tabPwd = box.querySelector("[data-tab-pwd]");
        const tabQr = box.querySelector("[data-tab-qr]");
        let qrTimer = null;

        const setTab = (active) => {
          tabPwd.style.background = active === "pwd" ? "var(--yy-accent-soft,#eef2ff)" : "var(--yy-muted,#f3f4f6)";
          tabPwd.style.color = active === "pwd" ? "var(--yy-glow,#6366f1)" : "var(--yy-text-2,#9aa6bc)";
          tabQr.style.background = active === "qr" ? "var(--yy-accent-soft,#eef2ff)" : "var(--yy-muted,#f3f4f6)";
          tabQr.style.color = active === "qr" ? "var(--yy-glow,#6366f1)" : "var(--yy-text-2,#9aa6bc)";
        };

        // 密码登录面板
        const renderPwd = () => {
          if (qrTimer) { clearInterval(qrTimer); qrTimer = null; }
          setTab("pwd");
          panel.innerHTML =
            '<div class="flex flex-col gap-2">' +
            '<input data-phone type="text" placeholder="手机号" class="h-9 rounded-lg border px-3 text-sm" style="border-color:var(--yy-border,#2a3348);color:var(--yy-text,#e8ecf4)">' +
            '<input data-password type="password" placeholder="密码" class="h-9 rounded-lg border px-3 text-sm" style="border-color:var(--yy-border,#2a3348);color:var(--yy-text,#e8ecf4)">' +
            '<div class="flex items-center gap-2">' +
            '<button type="button" data-login class="rounded-full px-5 py-1.5 text-sm text-white" style="background:var(--yy-accent,#6366f1)">登录</button>' +
            '<span class="text-xs" data-login-msg style="color:var(--yy-text-2,#9aa6bc)"></span>' +
            "</div></div>";
          const phone = panel.querySelector("[data-phone]");
          const password = panel.querySelector("[data-password]");
          const msg = panel.querySelector("[data-login-msg]");
          panel.querySelector("[data-login]").addEventListener("click", async () => {
            if (!phone.value || !password.value) {
              msg.textContent = "请输入手机号和密码";
              return;
            }
            msg.textContent = "登录中…";
            try {
              const r = await ctx.api.post("/login", { phone: phone.value, password: password.value });
              if (r.error) {
                msg.textContent = r.error;
              } else {
                renderStatus();
              }
            } catch (e) {
              msg.textContent = "登录失败：" + String(e);
            }
          });
        };

        // 扫码登录面板
        const renderQr = async () => {
          if (qrTimer) { clearInterval(qrTimer); qrTimer = null; }
          setTab("qr");
          panel.innerHTML =
            '<div class="flex flex-col items-center gap-3 py-2">' +
            '<div class="flex h-52 w-52 items-center justify-center rounded-lg border" style="border-color:var(--yy-border,#2a3348);background:#fff" data-qr-box>' +
            '<span class="text-xs" style="color:var(--yy-text-2,#9aa6bc)">二维码加载中…</span>' +
            "</div>" +
            '<p class="text-xs" data-qr-status style="color:var(--yy-text-2,#9aa6bc)">请用网易云音乐 APP 扫码</p>' +
            '<button type="button" data-qr-refresh class="rounded-full border px-4 py-1 text-xs" style="border-color:var(--yy-border,#2a3348);color:var(--yy-text,#e8ecf4)">刷新二维码</button>' +
            "</div>";
          const qrBox = panel.querySelector("[data-qr-box]");
          const qrStatus = panel.querySelector("[data-qr-status]");

          const loadQr = async () => {
            try {
              const r = await ctx.api.post("/qr-unikey", {});
              if (r.error) {
                qrBox.innerHTML = '<span class="text-xs" style="color:var(--yy-like,#ef4444)">' + r.error + "</span>";
                return;
              }
              qrBox.innerHTML = '<img src="' + r.qr_png + '" alt="二维码" class="h-48 w-48">';
              qrStatus.textContent = "请用网易云音乐 APP 扫码";
              // 轮询状态
              qrTimer = setInterval(async () => {
                try {
                  const c = await ctx.api.post("/qr-check", { unikey: r.unikey });
                  if (c.code === 803) {
                    clearInterval(qrTimer); qrTimer = null;
                    renderStatus();
                  } else if (c.code === 800) {
                    clearInterval(qrTimer); qrTimer = null;
                    qrStatus.textContent = "二维码已过期，请点击刷新";
                  } else if (c.code === 802) {
                    qrStatus.textContent = "已扫码，请在手机上确认";
                  } else if (c.code === 801) {
                    qrStatus.textContent = "等待扫码…";
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

        tabPwd.addEventListener("click", renderPwd);
        tabQr.addEventListener("click", renderQr);
        renderPwd();
      }
    } catch (e) {
      statusZone.textContent = "状态加载失败：" + String(e);
    }
  };

  // 搜索试播
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
      if (songs.length === 0) {
        searchResult.textContent = "无结果";
        return;
      }
      searchResult.innerHTML = "";
      for (const s of songs) {
        const row = document.createElement("div");
        row.className = "flex items-center gap-3 border-t py-2";
        row.style.borderColor = "var(--yy-border,#2a3348)";
        const info = document.createElement("div");
        info.className = "min-w-0 flex-1";
        info.innerHTML =
          '<p class="truncate text-sm" style="color:var(--yy-text,#e8ecf4)">' + s.name + " - " + (s.artist || "") + "</p>" +
          '<p class="truncate text-xs" style="color:var(--yy-text-2,#9aa6bc)">' + (s.album || "") + "</p>";
        const playBtn = document.createElement("button");
        playBtn.type = "button";
        playBtn.textContent = "▶ 试播";
        playBtn.className = "shrink-0 rounded-full border px-3 py-1 text-xs";
        playBtn.style.borderColor = "var(--yy-border,#2a3348)";
        playBtn.style.color = "var(--yy-text,#e8ecf4)";
        playBtn.addEventListener("click", () => preview.toggle(playBtn, null, () => ctx.api.post("/song-url", { id: s.id })));
        row.appendChild(info);
        row.appendChild(playBtn);
        searchResult.appendChild(row);
      }
    } catch (e) {
      searchResult.textContent = "搜索失败：" + String(e);
    }
  };

  searchBtn.addEventListener("click", doSearch);
  searchQ.addEventListener("keydown", (e) => {
    if (e.key === "Enter") doSearch();
  });

  renderStatus();

  return () => {
    preview.stop();
    box.remove();
  };
}
