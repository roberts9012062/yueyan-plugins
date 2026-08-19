# 月言图床 Worker（Cloudflare R2 + Workers）

boke「Cloudflare 图床」插件的服务端：一段可直接部署的 Worker 代码，
接收 boke 插件的上传并存入你的 R2 对象存储，公开 URL 回给站点展示。

## API 契约（boke 插件 ↔ Worker）

| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| GET | `/health` | Bearer API_KEY | 配对测试 → `{"ok":true}` |
| POST | `/upload` | Bearer API_KEY | multipart 字段 `file`（jpg/jpeg/png/gif/webp，≤10MB）→ `{"url","key","size","mime"}` |
| GET | `/f/:key` | 公开 | R2 流式读图（immutable 长缓存） |
| DELETE | `/f/:key` | Bearer API_KEY | 删除对象 |

对象键格式 `yyyymm/<16hex>.<ext>`（按月分目录 + 随机名，与 boke 本地媒体库同构）。

## 部署步骤

1. **创建 R2 桶**：Cloudflare Dashboard → R2 Object Storage → 创建存储桶（如 `yueyan-media`）
2. **准备本目录**：`cp wrangler.example.toml wrangler.toml`，把 `bucket` 改为你的桶名
3. **设置密钥**：生成一个长随机串（如 `openssl rand -hex 32`），执行
   `npx wrangler secret put API_KEY` 粘贴保存——这就是插件要配对的 API Key
4. **部署**：`npx wrangler deploy`，记下输出的 `https://<name>.<account>.workers.dev`
5. **站点配对**：boke 后台 → 插件 → Cloudflare 图床 → 设置，填 Worker URL 与 API Key，保存即自动测试配对

## 上传流向

```
boke 发帖/媒体库上传
  → boke 插件（Cloudflare 图床，URL/Key 已配对）
    → Worker POST /upload（Bearer Key）
      → R2 对象存储（yyyymm/随机名.jpg）
        → 返回公开 URL（https://<worker>/f/yyyymm/xxx.jpg）
          → 帖子正文/媒体库展示直连该 URL
```

## 说明

- 未配置 `API_KEY` 时所有鉴权端点一律拒绝（安全默认）；
- 图片扩展名白名单与大小限制（10MB）与 boke 本地媒体库一致；
- 公开读图默认经 Worker 代理（免自定义域）；绑定 R2 自定义域后可配 `PUBLIC_BASE` 切换直链；
- 删除端点供站点媒体删除联动（当前版本 boke 未调用，预留契约）。
