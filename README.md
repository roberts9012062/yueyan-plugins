# 月言官方插件库（yueyan-plugins）

> 本目录是 `roberts9012062/yueyan-plugins` 仓库内容的脚手架，与线上插件商城内容一一对应。
> 推送完成后可删除本目录（商城数据以 GitHub 仓库为准，项目内不保留）。

## 目录结构约定

- 每个插件一个文件夹，文件夹名 = 插件 ID（小写字母数字连字符）。
- 文件夹内必须包含：
  - `plugin.json`：插件元数据（字段见 `docs/plugin-development.md` 第 3 章）。
  - `README.md`：插件介绍（Markdown，商城「详情」弹窗渲染展示）。
- 仓库根可选 `market.json`：商城名称与描述（缺省时商城名取仓库名）。
- 插件 `.bpk` 安装包走插件源码仓库的 GitHub Release（`repo_url` 字段）。

## 推送方式

```bash
# 在 yueyan-plugins 仓库所在目录（或本目录作为 git 仓库）
git init
git remote add origin https://github.com/roberts9012062/yueyan-plugins.git
git add .
git commit -m "feat: 插件商城改为文件夹结构（每插件 plugin.json + README.md）"
git push origin main --force
```
