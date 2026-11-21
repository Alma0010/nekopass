# 发布与文档部署

本页供维护者使用。安装脚本和更新程序从 [Alma0010/nekopass](https://github.com/Alma0010/nekopass) 获取发布文件，文档托管在 [GitHub Pages](https://Alma0010.github.io/nekopass/)。

## 首次准备

把待发布的提交推送到默认分支 `main`。如果是新建的 fork，先在 GitHub 的 **Actions** 页面启用工作流。

## 发布新版本

### 从网页发版

1. 打开 [Publish release](https://github.com/Alma0010/nekopass/actions/workflows/release.yml)。
2. 点击 **Run workflow**，分支选择 `main`。
3. 在 `version` 输入一个未用过且比现有版本高的 `vX.Y.Z` 标签，点击 **Run workflow**。
4. 等待工作流完成。它会运行检查和测试，编译 Linux amd64 / arm64 的面板与 Agent，创建草稿、上传附件，再发布正式 Release 并设为最新版本。
5. 在 [Releases](https://github.com/Alma0010/nekopass/releases) 确认发布包和安装脚本已齐全。

工作流会创建标签和 Release。若已有同名标签或 Release，先查运行日志和现有草稿。合并上游时带来的标签也应保留。

### 用命令触发发版

也可以推送一个新标签来触发同一工作流。将示例版本号换成要发布的版本。

```powershell
$version = 'v0.21.0'
git tag $version
git push origin $version
```

发布由手动运行工作流或推送版本标签触发。

### 发布附件

工作流会上传：

- 面板：`nekopass-panel-linux-amd64.tar.gz`、`nekopass-panel-linux-arm64.tar.gz`。
- 节点：`nekopass-agent-linux-amd64`、`nekopass-agent-linux-arm64`。
- 安装和管理脚本：`install-panel.sh`、`install-agent.sh`、`nekopassctl.sh`、`nekopass-update.py`。
- 节点卸载脚本：`uninstall-agent.sh`。

仅上传代码或创建一个没有上述附件的 Release，安装及在线更新仍不能正常使用。`releases/latest/download/…` 需要已经发布的正式 Release；草稿和预发布版本不适合作为此处的默认安装源。

## 部署 GitHub Pages 文档站

1. 打开仓库 [Settings → Pages](https://github.com/Alma0010/nekopass/settings/pages)。
2. 在 **Build and deployment → Source** 选择 **GitHub Actions**。本项目已有工作流，无需新建 Jekyll 模板或选择 `main /docs`。
3. 打开 [Deploy documentation](https://github.com/Alma0010/nekopass/actions/workflows/docs.yml)，点击 **Run workflow**，选择 `main` 后运行。
4. 等待 `build` 和 `deploy` 成功，再打开 [文档站](https://Alma0010.github.io/nekopass/)。首次部署完成后，Settings → Pages 也会显示站点地址。

工作流构建 VitePress，自动设置 `/nekopass/` 子路径，只部署 `docs/.vitepress/dist/` 中的静态文件。以后修改 `docs/` 并推送到默认分支会自动更新网站，无需创建 Release，也无需将构建产物提交到 Git。

本地预览需要 Node.js 22.12+：

```powershell
npm --prefix docs ci
npm --prefix docs run dev
```

按 Pages 子路径构建并预览：

```powershell
$env:VITEPRESS_BASE = "/nekopass/"
npm --prefix docs run typecheck
npm --prefix docs run build
npm --prefix docs run preview
```

## 已安装的服务器

使用本仓库发布包安装的面板，按[更新面板](./manage.md#更新面板)中的步骤升级。

需要切换已有 Agent 的下载来源时，在面板修改[节点下载配置](./node.md#下载配置)，再到每台服务器执行新的安装命令。自定义实例继续使用原来的 `--service-name`。这样会一并更新 Agent 和下载组件。

## 常见问题

- **找不到 Run workflow**：先启用 fork 的 Actions，再确认工作流已在默认分支，并使用有仓库写权限的账号。
- **Pages 提示找不到站点或未启用**：在 Settings → Pages 选择 GitHub Actions，再重新运行文档工作流。
- **Actions 失败**：打开失败任务展开红色步骤查看日志。检查、构建或测试失败时不会发布完整版本；不要跳过检查后发布缺少附件的 Release。
- **首次发版前安装地址返回 404**：源码地址和 Release 附件不同；等待正式版本成功发布后再使用 `releases/latest/download/…`。

参考：[GitHub 手动运行工作流](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/manually-run-a-workflow)、[GitHub Pages 发布来源](https://docs.github.com/en/pages/getting-started-with-github-pages/configuring-a-publishing-source-for-your-github-pages-site)、[VitePress 部署](https://vitepress.dev/guide/deploy)。
