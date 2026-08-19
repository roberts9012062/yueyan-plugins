# 月言图床 Worker（Cloudflare Workers + R2）

[月言 boke](https://github.com/roberts9012062/yueyan-plugins) 博客「**CF图床**」插件的服务端程序。
部署到你的 Cloudflare 账号后，博客图片上传将直达你自己的 **R2 对象存储**——免费额度内（10GB 存储 / 每日百万级读）个人博客够用。

```
boke 发帖上传图片 → CF图床插件（本仓库 Worker 的 URL + Key）
  → Cloudflare Worker → R2 对象存储 → 公开 URL 回博客展示
```

## 部署（约 5 分钟）

### 前置准备

1. 注册 [Cloudflare 账号](https://dash.cloudflare.com/sign-up)（免费）
2. 本机安装 [Node.js](https://nodejs.org/)（18+）

### 第一步：创建 R2 存储桶

Cloudflare Dashboard → **R2 Object Storage** → 创建存储桶，命名为 `yueyan-media`（名字可自定，后面配置保持一致即可）。

> R2 首次开通需绑定支付方式（有免费额度，不扣费）。

### 第二步：部署本 Worker

```bash
# 1. 获取源码（本仓库）
git clone https://github.com/roberts9012062/yueyan-image-worker.git
cd yueyan-image-worker

# 2. 生成本地配置（把桶名改成你自己的）
cp wrangler.example.toml wrangler.toml

# 3. 登录 Cloudflare（会打开浏览器授权）
npx wrangler login

# 4. 设置访问密钥（生成一个长随机串并保存好——博客插件配置要用）
openssl rand -hex 32
npx wrangler secret put API_KEY    # 粘贴上面生成的串

# 5. 部署
npx wrangler deploy
```

部署成功会输出一个 `https://yueyan-image-bed.<你的子域>.workers.dev` 地址。

### 第三步：（推荐）绑定自定义域

`workers.dev` 域名在中国大陆访问不稳定。如果你有托管在 Cloudflare 的域名，强烈建议绑定自定义域：

编辑 `wrangler.toml`，在**文件顶部**（所有 `[...]` 段之前）加：

```toml
routes = [
  { pattern = "imgs.你的域名.com", custom_domain = true }
]

[vars]
PUBLIC_BASE = "https://imgs.你的域名.com"
```

再执行一次 `npx wrangler deploy`（自动建 DNS 记录与 HTTPS 证书，约 1-3 分钟生效）。

### 第四步：博客配对

boke 后台 → 插件商城 → 安装「**CF图床**」→ 侧栏「插件 → CF图床 → 设置」：

| 配置项 | 填什么 |
|--------|--------|
| Workers URL | `https://imgs.你的域名.com`（或 workers.dev 地址） |
| API Key | 第二步 `wrangler secret put API_KEY` 保存的那个串 |

保存后即配对成功——发帖上传的图片将直达你的 R2。

## API 契约

| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| GET | `/health` | Bearer API_KEY | 配对测试 → `{"ok":true}` |
| POST | `/upload` | Bearer API_KEY | multipart 字段 `file`（jpg/jpeg/png/gif/webp，≤10MB）→ `{"url","key","size","mime"}` |
| GET | `/list` | Bearer API_KEY | 对象列表（`?cursor=` 分页，每页 60） |
| GET | `/f/:key` | 公开 | R2 流式读图（immutable 长缓存） |
| DELETE | `/f/:key` | Bearer API_KEY | 删除对象 |

对象键格式 `yyyymm/<16hex>.<ext>`（按月分目录 + 随机名）。

## 常见问题

**Q：上传报"图床上传失败：Worker 请求失败"？**
A：检查 Workers URL 是否填对、自定义域 DNS 是否已生效；国内网络下 workers.dev 地址大概率不可达，请绑定自定义域。

**Q：配对测试提示 invalid key？**
A：博客插件里填的 API Key 与 `wrangler secret put API_KEY` 设置的值不一致，重新核对。

**Q：改了压缩参数怎么生效？**
A：压缩参数（质量/最大边长）在**博客插件的设置页**配置，与 Worker 无关，保存即生效。

**Q：想限制上传来源怎么办？**
A：API Key 本身就是准入凭据，不外泄即安全；如需进一步限制可在 Worker 里按需求扩展（如来源校验）。

## 相关链接

- 插件本体与安装：[yueyan-plugins 插件库](https://github.com/roberts9012062/yueyan-plugins)（boke 后台插件商城搜「CF图床」）
- [Cloudflare Workers 文档](https://developers.cloudflare.com/workers/)
- [R2 定价与免费额度](https://developers.cloudflare.com/r2/pricing/)
