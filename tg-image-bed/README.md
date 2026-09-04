# TG图床

Telegram 频道图床：把博客图片存进 Telegram 频道（Bot API），访客经你自备的反代 Worker 读取——**Bot Token 全程只在服务端，浏览器永远拿不到**。灵感来自开源项目 [telegraph-Image](https://github.com/x-dr/telegraph-Image) 的 TG 渠道模式，机制对齐官方 `CF图床`（image-cdn）插件的三件套架构。

## 功能特性

- **原图保真**：默认 `sendDocument` 模式，图片不经压缩直达 TG 频道；可切换 `photo` 模式（TG 服务端压缩，省流量）
- **后台图库**：网格浏览、点击/拖拽/文件夹批量上传、复制 Markdown / URL、单选批量删除
- **Token 安全**：Bot Token 只存插件设置与你的 Worker secret；帖子里的图片 URL 形如 `https://img.example.com/f/{file_id}`，`file_id` 是公开标识，泄露无风险
- **配对探测**：图库页实时显示 Bot / 频道 / Worker 三方配对状态（`getMe` + `getChat` + Worker `/health`）
- **大陆服务器友好**：可选 `api_proxy` 设置项，插件进程经 HTTP 代理访问 `api.telegram.org`

## 前置准备（三件套）

| 组件 | 怎么来 | 作用 |
|---|---|---|
| Telegram Bot | [@BotFather](https://t.me/BotFather) 创建，拿到 Token | 上传通道 |
| Telegram 频道/群 | 把 Bot 拉入并设为**管理员** | 图片存储桶 |
| 反代 Worker | **开源仓库部署：[tg-image-bed-worker](https://github.com/roberts9012062/tg-image-bed-worker)**（wrangler 三步部署 + Dashboard 零命令行两种教程） | 访客匿名读图（持 token 反代 + 缓存） |

## 安装与配置

1. 后台「插件商城」安装 TG图床（或本地上传 `.bpk`）；
2. 「我的插件 → TG图床 → 设置」填写三项配对：
   - **Bot Token**：`123456:AAxxx...`
   - **Chat ID**：`-1001234567890`（私有频道）或 `@channel`（公开频道）
   - **反代 Worker 地址**：`https://img.example.com`
3. 打开后台侧栏「TG图床」图库页，顶部显示 `✓ Bot @xxx · 频道 xxx · Worker 正常` 即配对成功；
4. 上传图片 → 点击图片卡片上的「复制 MD」→ 粘贴进帖子正文。

**Chat ID 怎么找**：把频道消息转发给 [@VersaToolsBot](https://t.me/VersaToolsBot) 等工具机器人即可获得；或先用 Bot 所在频道发一条消息，看 `getUpdates` 返回的 `chat.id`。

## 使用说明

- **上传**：图库页点击选择或拖拽图片/文件夹（多选、递归收集）；上传成功后网格置顶显示
- **插图**：每张图提供「复制 MD」（`![文件名](URL)`）与「复制 URL」两个按钮，复制后粘贴到发帖正文
- **删除**：点击图片选中（可多选）→「删除选中」；插件会尽力删除频道消息并移除本地图库记录
- **发送模式**：插件设置中切换——`document` 原图保真（默认）；`photo` 由 TG 压缩重编码（gif 会自动回退 document，因 sendPhoto 不支持动图）
- **图片体检（v0.4.0）**：图库页右上「图片体检 →」进入——扫描全部已发布说说/文章的正文图片，
  自动分类「外部图片（外链，易失效）/ 本地图片（`/media/`，占服务器空间）/ 已TG」；
  勾选后一键转存到 TG 图床并自动把帖子正文里的旧链接替换为 TG 直链
  （外部图由插件后端下载后转存，本地图由浏览器读取后上传；替换走后台 `PUT /admin/posts/:id`，
  仅管理员或帖子作者可操作，其余内容原样保留、本地源文件不删除）

## 限制与 FAQ

| 项 | 说明 |
|---|---|
| 单图 ≤ 20MB | Telegram Bot API 文件下载上限，上传时前置校验并明确报错 |
| webp 图变"贴纸"？ | TG 服务器会把符合贴纸规格的 webp 自动转为贴纸存储（响应无 document 字段）——插件已兼容（v0.4.2），直链照常访问 |
| gif 动图变 mp4？ | TG 会把 gif 自动转码为 mp4 动画存储（Bot API 固有行为），直链下载到的是 mp4（正文 `<img>` 无法显示动图，如需动效请用 video 嵌入或改用其他格式） |
| 删除后旧 URL 还能访问？ | 删除会移除频道消息与本地图库记录，但 TG 服务器缓存文件可能仍可经旧 URL 访问一段时间（无法从 Bot API 强制清除） |
| 服务器连不上 TG？ | 博客服务器在中国大陆时无法直连 `api.telegram.org`，在插件设置填 `api_proxy`（如 `http://127.0.0.1:7890`）；访客访问的是 Cloudflare Worker（海外节点），一般不受影响 |
| Token 泄露了怎么办？ | 立即在 @BotFather 执行 `/revoke` 吊销旧 Token → 重新生成 → 更新插件设置与 Worker secret |
| 与 CF图床冲突吗？ | 不冲突可共存；两者均为独立图库形态，宿主媒体上传的接管（seam）当前由 CF图床承担，本插件的 `/storage/*` 契约端点已实现、备用兼容 |
| 上传历史存在哪？ | 插件数据目录 `data/plugins/tg-image-bed/history.json`（卸载重装不丢失） |

## 开放接口（外部应用对接）

v0.3.0 起声明开放端点（宿主 v1.4.1+ 声明式开放端点）：安装/升级后自动进入后台「接口开放」目录，站长创建 API Key 并勾选授权后，外部应用（如月言浏览器插件）即可凭 `X-Api-Key` 调用：

| 端点 | 方法 | 说明 |
|---|---|---|
| `/api/v1/open/plugins/tg-image-bed/upload` | POST | 上传图片（`{filename, mime, content_b64}`）→ `{url, markdown, file_id, size, mime}` |
| `/api/v1/open/plugins/tg-image-bed/list` | POST | 图库分页（`{cursor}` 留空=首页）→ `{objects[], cursor}` |

调用示例：

```js
const res = await fetch(`${siteOrigin}/api/v1/open/plugins/tg-image-bed/upload`, {
  method: "POST",
  headers: { "Content-Type": "application/json", "X-Api-Key": apiKey },
  body: JSON.stringify({ filename: "a.png", mime: "image/png", content_b64 }),
});
const body = await res.json(); // {code, message, data, request_id}，code=0 成功，data.url 即图片地址
```

注意：响应语义为开放网关统一包络（`{code:0,data}`；插件侧 `error` 转网关 400）；建议只给浏览器插件授权所需端点，Key 泄露时在后台删除重建即可。

## 隐私与安全

- Bot Token / Chat ID 存宿主插件设置（`sdk.Config` 下发），仅插件进程内存使用；
- 反代 Worker 的 Token 存 Cloudflare secret，仅在 Worker 服务端用于回源；
- 访客可见的唯一标识是 `file_id`（Telegram 公开文件标识，非机密）；
- 上传/列表需登录，删除仅管理员（插件 API 权限模型）。
