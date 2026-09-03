# TG图床 · 反代 Worker 部署指南

访客的浏览器无法直接读 Telegram 文件（直链含 Bot Token，绝不能暴露）。本 Worker 是你部署在 Cloudflare 上的一层只读反代：**持 Token 回源、对访客只暴露 `file_id`**，配 Cloudflare 缓存扛流量。部署约 3 分钟，免费额度足够个人博客图床使用。

## 部署步骤

```bash
# 0) 准备：一个 Cloudflare 账号 + 本目录（index.js / wrangler.example.toml）
# 1) 复制配置并改名（name 决定默认域名 <name>.<账号>.workers.dev）
cp wrangler.example.toml wrangler.toml

# 2) 登录 Cloudflare（浏览器授权）
npx wrangler login

# 3) 注入 Bot Token 为 secret（@BotFather 创建的 Token；不要写进任何文件）
npx wrangler secret put TG_BOT_TOKEN

# 4) 部署
npx wrangler deploy
```

部署成功会输出 `https://tg-image-bed-worker.<你的账号>.workers.dev`。

## 配对到博客

1. 博客后台「我的插件 → TG图床 → 设置」；
2. 「反代 Worker 地址」填 `https://tg-image-bed-worker.<你的账号>.workers.dev`（自定义域名同理，填 `https://img.你的域名`）；
3. 打开后台「TG图床」图库页，顶部横幅显示 `✓ 配对正常 · … · Worker 正常` 即成功。

## 路由契约

| 路由 | 说明 |
|---|---|
| `GET /health` | 存活探测（插件配对检测用；未配置 Token 返回 500 + 提示） |
| `GET /f/{file_id}` | 读图：`getFile` 解析临时 `file_path` → 持 Token 回源 → CF 缓存 → 返回访客 |

## 安全说明

- **Token 只存 Cloudflare secret**：仓库与本目录的任何文件都不含 Token；Worker 日志默认也不打印 Token；
- **访客只见 file_id**：`file_id` 是 Telegram 公开文件标识，泄露无风险（无法反查出 Token）；
- **泄露处置**：@BotFather `/revoke` 吊销旧 Token → 重新生成 → `wrangler secret put TG_BOT_TOKEN` 更新 → 同步更新博客插件设置；
- **缓存语义**：同一 `file_id` 的文件内容不可变，Worker 侧长期缓存；浏览器侧 `max-age=86400`。

## 可选：绑定自定义域名

workers.dev 域名在部分网络环境可能被污染，建议绑定自有域名（Cloudflare Dashboard → Workers → 该 Worker → Settings → Domains & Routes → Add Custom Domain）。自定义域名需托管在同一 Cloudflare 账号。
