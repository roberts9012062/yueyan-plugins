# Cloudflare 图床

> 媒体上传直达 Cloudflare R2 对象存储：填 Workers URL 与 Key 配对即用，公开 URL 回站展示。免费。

## 功能特性

- **上传直达 R2**：配对后站点图片上传不再落本地磁盘，直接存入你的 Cloudflare R2 桶；
- **公开 URL 回站**：R2 对象经 Worker 公开代理（`/f/{key}`，immutable 长缓存），帖子与媒体库直接引用；
- **配对即用**：插件只需两步配置——Workers URL + API Key，保存后自动接管图片上传；
- **平滑降级**：Worker 不可达或未配对时上传自动回退本地存储，站点不断图。

## 安装与配对（三步）

1. **部署 Worker**：按 `worker/README.md` 部署参考实现到你的 Cloudflare 账号（创建 R2 桶 → `wrangler secret put API_KEY` → `wrangler deploy`）；
2. **安装插件**：后台「插件商城」→「Cloudflare 图床」→ 免费安装；
3. **填配对信息**：侧栏「插件 → Cloudflare 图床 → 设置」，填 Worker URL 与 API Key 保存。

## 工作原理

```
发帖/媒体库上传图片
  → boke 宿主（类型/大小白名单校验，与本地一致）
    → 插件（media.storage 存储接缝接管）
      → Worker POST /upload（Bearer API Key）
        → R2 对象存储（yyyymm/随机名.jpg）
          → 公开 URL 落库 media_assets，帖子展示直连
```

- 仅**图片**走图床（jpg/jpeg/png/gif/webp，≤10MB）；音频/视频仍走本地存储；
- 插件停用/卸载即回归本地上传（接缝注册可逆），历史 R2 图片 URL 不受影响；
- Worker 参考实现与部署配置在 [worker/](worker/README.md) 目录。

## 配置说明

| 设置项 | 说明 |
|--------|------|
| Workers URL | 部署的 Worker 地址（https:// 开头） |
| API Key | Worker 的 `API_KEY` secret（`wrangler secret put API_KEY` 设置的值） |
