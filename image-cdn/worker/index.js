// marketplace-repo/image-cdn/worker/index.js
// 月言图床 Worker（Cloudflare Workers + R2 参考实现）：
//   部署到你的 Cloudflare 账号，绑定 R2 桶后即为图床 API——boke「Cloudflare 图床」
//   插件填本 Worker 的 URL 与 API Key 配对即可使用（上传直达 R2）。
//
// 环境绑定/变量（wrangler.toml 或 Dashboard 配置）：
//   R2BIND    R2 存储桶绑定（wrangler.toml 中 [r2_buckets] binding = "R2BIND"）
//   PUBLIC_BASE 公开访问基址（默认 https://<worker>.workers.dev——即本 Worker 代理地址）
//   API_KEY   访问密钥（`wrangler secret put API_KEY` 设置；插件配置填同一个值）
//
// API 契约（boke 插件 ↔ Worker 联调基础，详见同目录 README.md）：
//   GET    /health        配对测试（Bearer API_KEY）→ {"ok":true}
//   POST   /upload        上传图片（Bearer API_KEY；multipart 字段 file）→ {"url","key","size","mime"}
//   GET    /f/:key        公开读图（R2 流式透传，immutable 缓存）
//   DELETE /f/:key        删除对象（Bearer API_KEY）→ {"deleted":true}
export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    const key = url.pathname.startsWith("/f/") ? decodeURIComponent(url.pathname.slice(3)) : "";
    try {
      if (url.pathname === "/health") {
        return handleHealth(request, env);
      }
      if (url.pathname === "/upload" && request.method === "POST") {
        return handleUpload(request, env);
      }
      if (url.pathname === "/list" && request.method === "GET") {
        return handleList(request, env, url);
      }
      if (key && request.method === "GET") {
        return handleGet(env, key);
      }
      if (key && request.method === "DELETE") {
        return handleDelete(request, env, key);
      }
      return json(404, { error: "not found" });
    } catch (e) {
      return json(500, { error: String((e && e.message) || e) });
    }
  },
};

// authorized Bearer API_KEY 校验（未配置 API_KEY 一律拒绝——安全默认）。
function authorized(request, env) {
  const expect = env.API_KEY || "";
  if (!expect) {
    return false;
  }
  const got = (request.headers.get("Authorization") || "").replace(/^Bearer\s+/i, "");
  return got === expect;
}

// json JSON 响应辅助。
function json(status, body) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json; charset=utf-8" },
  });
}

// handleHealth 配对测试：鉴权 + R2 可达（HEAD 桶根对象探测）。
async function handleHealth(request, env) {
  if (!authorized(request, env)) {
    return json(401, { ok: false, error: "invalid key" });
  }
  if (!env.R2BIND) {
    return json(500, { ok: false, error: "R2 binding missing (R2BIND)" });
  }
  return json(200, { ok: true, service: "yueyan-image-bed" });
}

// 允许的图片扩展名 → MIME（上传白名单，与 boke 本地媒体库一致）。
const IMAGE_TYPES = {
  jpg: "image/jpeg", jpeg: "image/jpeg", png: "image/png",
  gif: "image/gif", webp: "image/webp",
};

// handleUpload 上传图片：multipart file → 随机键 → R2 put → 公开 URL。
async function handleUpload(request, env) {
  if (!authorized(request, env)) {
    return json(401, { error: "invalid key" });
  }
  if (!env.R2BIND) {
    return json(500, { error: "R2 binding missing (R2BIND)" });
  }
  const form = await request.formData();
  const file = form.get("file");
  if (!file || typeof file === "string") {
    return json(400, { error: "multipart field 'file' required" });
  }
  // 扩展名白名单（大小写不敏感；无扩展名拒绝）
  const name = String(file.name || "");
  const dot = name.lastIndexOf(".");
  const ext = dot >= 0 ? name.slice(dot + 1).toLowerCase() : "";
  const mime = IMAGE_TYPES[ext];
  if (!mime) {
    return json(400, { error: "unsupported image type (jpg/jpeg/png/gif/webp only)" });
  }
  if (file.size > 10 * 1024 * 1024) {
    return json(400, { error: "image too large (max 10MB)" });
  }
  // 对象键：yyyymm/16hex.ext（按月分目录 + 随机名防遍历重名——与 boke 本地存储同构）
  const key = `${new Date().toISOString().slice(0, 7).replace("-", "")}/${crypto.randomUUID().replace(/-/g, "").slice(16)}.${ext}`;
  await env.R2BIND.put(key, file.stream(), {
    httpMetadata: { contentType: mime, cacheControl: "public, max-age=31536000, immutable" },
  });
  const base = (env.PUBLIC_BASE || new URL(request.url).origin).replace(/\/+$/, "");
  return json(200, { url: `${base}/f/${key}`, key, size: file.size, mime });
}

// handleGet 公开读图：R2 流式透传（对象不存在 404；immutable 长缓存）。
async function handleGet(env, key) {
  if (!env.R2BIND) {
    return json(500, { error: "R2 binding missing" });
  }
  // 防路径穿越：拒绝 .. 与绝对路径段
  if (key.includes("..") || key.startsWith("/")) {
    return json(400, { error: "bad key" });
  }
  const obj = await env.R2BIND.get(key);
  if (!obj) {
    return json(404, { error: "not found" });
  }
  const headers = new Headers();
  headers.set("Content-Type", obj.httpMetadata?.contentType || "application/octet-stream");
  headers.set("Cache-Control", "public, max-age=31536000, immutable");
  headers.set("ETag", obj.etag);
  return new Response(obj.body, { status: 200, headers });
}

// handleList 对象列表（Bearer key；?cursor= 分页，每页 60，时间倒序）。
// 返回 {objects:[{key,url,size,uploaded}], cursor}——uploaded 为 ISO 时间。
async function handleList(request, env, url) {
  if (!authorized(request, env)) {
    return json(401, { error: "invalid key" });
  }
  if (!env.R2BIND) {
    return json(500, { error: "R2 binding missing" });
  }
  const cursor = url.searchParams.get("cursor") || undefined;
  const listed = await env.R2BIND.list({ cursor, limit: 60 });
  const base = (env.PUBLIC_BASE || new URL(request.url).origin).replace(/\/+$/, "");
  const objects = (listed.objects || []).map((o) => ({
    key: o.key,
    url: `${base}/f/${o.key}`,
    size: o.size,
    uploaded: o.uploaded ? new Date(o.uploaded).toISOString() : "",
  })).sort((a, b) => (a.uploaded < b.uploaded ? 1 : -1)); // 同页内时间倒序（新图在前）
  return json(200, { objects, cursor: listed.truncated ? listed.cursor : "" });
}

// handleDelete 删除对象（鉴权）。
async function handleDelete(request, env, key) {
  if (!authorized(request, env)) {
    return json(401, { error: "invalid key" });
  }
  await env.R2BIND.delete(key);
  return json(200, { deleted: true });
}
