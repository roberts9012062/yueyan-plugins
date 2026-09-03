// marketplace-repo/tg-image-bed/worker/index.js
// TG图床反代 Worker（Cloudflare Workers 参考实现，~100 行）：
//   访客经本 Worker 匿名读取 Telegram 频道图片——Bot Token 只存在本 Worker 的
//   secret 中，浏览器永远拿不到（机制对齐 telegraph-Image 的 cfile 路由）。
//
// 环境变量（Dashboard 或 wrangler secret 配置）：
//   TG_BOT_TOKEN  Bot Token（`wrangler secret put TG_BOT_TOKEN`；必填）
//
// 路由契约（boke「TG图床」插件填本 Worker 地址即配对）：
//   GET /health       存活探测 → {"ok":true}（插件配对探测用，免鉴权）
//   GET /f/{file_id}  读图：getFile 解析临时 file_path → 持 token 回源 → CF 缓存后返回
export default {
  async fetch(request, env, ctx) {
    const url = new URL(request.url);
    try {
      if (url.pathname === "/health") {
        if (!env.TG_BOT_TOKEN) {
          return json(500, { ok: false, error: "TG_BOT_TOKEN not set (wrangler secret put TG_BOT_TOKEN)" });
        }
        return json(200, { ok: true, service: "tg-image-bed-worker" });
      }
      if (url.pathname.startsWith("/f/") && request.method === "GET") {
        return await handleFile(request, env, ctx, decodeURIComponent(url.pathname.slice(3)));
      }
      return json(404, { error: "not found" });
    } catch (e) {
      return json(500, { error: String((e && e.message) || e) });
    }
  },
};

// json JSON 响应辅助。
function json(status, body) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json; charset=utf-8" },
  });
}

// file_id 形态（字母数字下划线连字符，适中长度）——拒绝任意注入（路径/查询串等）。
const FILE_ID_RE = /^[A-Za-z0-9_-]{10,300}$/;

// contentTypeOf 按 Telegram file_path 扩展名推断 MIME（对齐 telegraph-Image cfile）。
function contentTypeOf(filePath) {
  const ext = String(filePath).split(".").pop().toLowerCase();
  const map = {
    jpg: "image/jpeg", jpeg: "image/jpeg", png: "image/png",
    gif: "image/gif", webp: "image/webp", svg: "image/svg+xml",
  };
  return map[ext] || "application/octet-stream";
}

// getFile 持 token 解析 file_id → 临时 file_path（每次访问实时解析——TG 直链有时效）。
async function resolveFilePath(env, fileId) {
  const res = await fetch(
    `https://api.telegram.org/bot${env.TG_BOT_TOKEN}/getFile?file_id=${encodeURIComponent(fileId)}`,
    { headers: { "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36" } }
  );
  const data = await res.json();
  if (!data.ok) {
    throw new Error("getFile failed: " + (data.description || res.status));
  }
  return data.result.file_path;
}

// handleFile 读图主流程：缓存命中直返 → getFile → 持 token 回源 → 写缓存 → 返回。
async function handleFile(request, env, ctx, fileId) {
  if (!env.TG_BOT_TOKEN) {
    return json(500, { error: "TG_BOT_TOKEN not set" });
  }
  if (!FILE_ID_RE.test(fileId)) {
    return json(400, { error: "bad file_id" });
  }
  // Cloudflare Cache API：命中则连 getFile 都省（图片内容不可变，file_id 即内容键）
  const cache = caches.default;
  const cached = await cache.match(request);
  if (cached) {
    return cached;
  }
  const filePath = await resolveFilePath(env, fileId);
  const upstream = await fetch(`https://api.telegram.org/file/bot${env.TG_BOT_TOKEN}/${filePath}`);
  if (!upstream.ok) {
    return json(upstream.status === 404 ? 404 : 502, { error: "telegram fetch failed: " + upstream.status });
  }
  const buf = await upstream.arrayBuffer();
  const headers = new Headers();
  headers.set("Content-Type", contentTypeOf(filePath));
  headers.set("Cache-Control", "public, max-age=86400"); // 浏览器/CDN 一天（Worker 侧缓存长期持有）
  headers.set("Access-Control-Allow-Origin", "*");
  const resp = new Response(buf, { status: 200, headers });
  ctx.waitUntil(cache.put(request, resp.clone()));
  return resp;
}
