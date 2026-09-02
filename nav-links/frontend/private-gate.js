// nav-links/frontend/private-gate.js
// 私有导航门禁流程模块（nav-page 页内切换与 private-page 直达页共用）：
//   试取私有数据（管理员 Bearer / 解锁 token）→ 成功回调 onGranted(data)；
//   self 模式无权限 → 「仅站长可见」卡；password 模式 → 密码解锁卡（错误提示 + 重试）。
// 数据经宿主桥接 /api/v1/nav/private/**；解锁 token 存 localStorage（7 天，密码变更后失效）。
import { escapeHtml } from "/plugin-sdk/shared.js";

// tokenStorageKey 解锁 token 存储键。
const tokenStorageKey = "nav-links-private-token";

// bearerHeaders 读宿主登录态（管理员直通判定；无则匿名访问）。
function bearerHeaders() {
  try {
    const raw = localStorage.getItem("yueyan-tokens");
    const accessToken = raw ? JSON.parse(raw).access_token || "" : "";
    return accessToken ? { Authorization: "Bearer " + accessToken } : {};
  } catch (e) {
    return {};
  }
}

// fetchPrivateLinks 取私有数据（Bearer + X-Nav-Token 双通道凭证，命中即放行）。
async function fetchPrivateLinks() {
  const headers = Object.assign({ Accept: "application/json" }, bearerHeaders());
  const token = localStorage.getItem(tokenStorageKey);
  if (token) {
    headers["X-Nav-Token"] = token;
  }
  const res = await fetch("/api/v1/nav/private/links", { headers });
  const body = await res.json().catch(() => ({}));
  return { status: res.status, body: body };
}

// fetchPrivateMeta 取门禁元数据（模式/是否已设密码/标题/私有条数；公开无敏感）。
async function fetchPrivateMeta() {
  const res = await fetch("/api/v1/nav/private/meta", { headers: { Accept: "application/json" } });
  if (!res.ok) {
    throw new Error("HTTP " + res.status);
  }
  return res.json();
}

// cardShell 门禁卡片骨架（标题 + 正文插槽；纯函数）。
function cardShell(meta, inner) {
  return (
    '<div style="max-width:400px;margin:50px auto 0;padding:28px 26px;border-radius:16px;border:1px solid var(--nl-border,#2a3348);background:var(--nl-elev,#121826);text-align:center">' +
    '<div style="font-size:34px;line-height:1">🔒</div>' +
    '<h1 style="margin:14px 0 4px;font-size:20px;font-weight:800;color:var(--nl-text,#e8ecf4)">' + escapeHtml(meta.title || "私有导航") + "</h1>" +
    '<p style="margin:0 0 18px;font-size:13px;color:var(--nl-text2,#9aa6bc)">' + escapeHtml(meta.subtitle || "仅对站长与获准访客可见的收藏站点") +
    (meta.count ? "（共 " + meta.count + " 个站点）" : "") + "</p>" +
    inner +
    "</div>"
  );
}

// loadPrivateView 在 mount 内执行完整门禁流程。
// onGranted(data)：授权通过（mount 已清空，data = {links, categories, tags, settings}），
// 调用方在回调里渲染看板。返回 cleanup()：中止异步回调并清空 mount。
export function loadPrivateView(mount, onGranted) {
  let disposed = false;

  const grant = (data) => {
    if (disposed) {
      return;
    }
    mount.innerHTML = ""; // 清门禁 UI，交出容器给看板
    onGranted(data);
  };

  // renderSelfGate self 模式拒绝：仅提示，无密码通道。
  const renderSelfGate = (meta) => {
    mount.innerHTML = cardShell(
      meta,
      '<p style="margin:0;font-size:13px;color:var(--nl-text2,#9aa6bc)">此导航仅站长可见，请以站长账号登录后访问。</p>'
    );
  };

  // renderPasswordGate password 模式：密码输入 + 解锁（message 为错误/过期提示）。
  const renderPasswordGate = (meta, message) => {
    mount.innerHTML = cardShell(
      meta,
      (message ? '<p style="margin:0 0 10px;font-size:12px;color:#ef4444">' + escapeHtml(message) + "</p>" : "") +
        '<input data-pw type="password" placeholder="输入访问密码…" autocomplete="current-password" style="height:40px;width:100%;border-radius:999px;border:1px solid var(--nl-border,#2a3348);background:transparent;color:var(--nl-text,#e8ecf4);padding:0 18px;font-size:13px;outline:none;box-sizing:border-box;text-align:center">' +
        '<button type="button" data-unlock style="margin-top:12px;height:40px;width:100%;border-radius:999px;border:none;background:var(--nl-accent,#a8b8d8);color:var(--nl-on-accent,#0b0f1a);font-size:13px;font-weight:600;cursor:pointer">解锁访问</button>'
    );
    const pwEl = mount.querySelector("[data-pw]");
    pwEl.focus();
    const attemptUnlock = async () => {
      const password = pwEl.value;
      if (!password) {
        pwEl.focus();
        return;
      }
      try {
        const res = await fetch("/api/v1/nav/private/unlock", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ password: password }),
        });
        const body = await res.json().catch(() => ({}));
        if (disposed) {
          return;
        }
        if (res.status === 200 && body.token) {
          localStorage.setItem(tokenStorageKey, body.token);
          load(); // 凭新 token 重取数据
          return;
        }
        renderPasswordGate(meta, body.error || "访问密码不正确");
      } catch (e) {
        if (!disposed) {
          renderPasswordGate(meta, "解锁请求失败，请稍后重试");
        }
      }
    };
    mount.querySelector("[data-unlock]").addEventListener("click", attemptUnlock);
    pwEl.addEventListener("keydown", (ev) => {
      if (ev.key === "Enter") {
        attemptUnlock();
      }
    });
  };

  // load 主流程：先试取数（管理员/有效 token 直进），失败按错误码渲染门禁。
  const load = async () => {
    mount.innerHTML = '<p style="text-align:center;padding:50px 0;font-size:13px;color:var(--nl-text2,#9aa6bc)">正在加载私有导航…</p>';
    try {
      const r = await fetchPrivateLinks();
      if (disposed) {
        return;
      }
      if (r.status === 200) {
        grant(r.body);
        return;
      }
      const meta = await fetchPrivateMeta().catch(() => ({}));
      if (disposed) {
        return;
      }
      if (r.status === 403 || r.body.code === "self_only") {
        renderSelfGate(meta);
        return;
      }
      const stale = localStorage.getItem(tokenStorageKey);
      renderPasswordGate(meta, stale ? "解锁已过期或访问方式已变更，请重新输入访问密码" : "");
    } catch (e) {
      if (!disposed) {
        mount.innerHTML = '<p style="text-align:center;padding:50px 0;font-size:13px;color:var(--nl-text2,#9aa6bc)">私有导航加载失败，请刷新重试</p>';
      }
    }
  };

  load();
  return () => {
    disposed = true;
    mount.innerHTML = "";
  };
}
