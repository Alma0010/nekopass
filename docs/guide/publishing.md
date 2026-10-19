# 发布与文档部署

本 fork 的仓库是 [Alma0010/nekopass](https://github.com/Alma0010/nekopass)，安装、检查更新和下载发布文件均使用此仓库。文档站部署地址为 [Alma0010.github.io/nekopass](https://Alma0010.github.io/nekopass/)，首次部署成功后可访问。

## 首次准备

1. 将本地修改提交并推送到 `main`。
2. 打开仓库的 **Actions**。如果 fork 的工作流尚未启用，点击 **I understand my workflows, go ahead and enable them**。
3. 在 Actions 左侧可看到 **Publish release** 和 **Deploy documentation**。若找不到手动运行按钮，确认对应工作流已推送到仓库的默认分支。

在仓库目录的 PowerShell 中提交和推送：

```powershell
git status
git add .
git commit -m "将安装更新与文档切换到 Alma0010 仓库"
git push origin main
```

## 发布 Releases

### 从网页发版

1. 打开 [Publish release](https://github.com/Alma0010/nekopass/actions/workflows/release.yml)。
2. 点击 **Run workflow**，分支选择 `main`。
3. 在 `version` 输入新的正式版本号，例如首次 fork 发版可使用 `v0.19.1`，然后点击 **Run workflow**。
4. 等待工作流完成。它会运行检查和测试，编译 Linux amd64 / arm64 的面板与 Agent，创建草稿、上传附件，再发布正式 Release 并设为最新版本。
5. 在 [Releases](https://github.com/Alma0010/nekopass/releases) 确认发布包和安装脚本已齐全。

版本号只接受 `vX.Y.Z`，例如 `v0.19.1`；当前更新器不接受 `-alma.1` 等后缀。每次发版使用一个未使用过、数值递增的版本号。本地合并上游后可能已有上游标签，尤其 `v0.19.0` 指向上游原始提交，不能拿来标记本 fork 的修改。工作流会拒绝指向其他提交的已有标签。不要删除或强制覆盖上游标签。

这个工作流会自动创建新标签及 Release，无需先在 Releases 页面手动创建同名版本。同名 Release 已存在时，创建步骤会失败；应先检查运行日志与已有草稿，再决定后续操作。

### 用命令触发发版

也可在已提交并推送代码后创建一个全新的标签，标签推送会自动运行同一发版工作流。以下与网页方式二选一：

```powershell
git tag v0.19.1
git push origin v0.19.1
```

不要照抄一个已存在的版本号。推送代码到 `main` 本身不会发布 Release。

### 发布附件

工作流会上传：

- 面板：`nekopass-panel-linux-amd64.tar.gz`、`nekopass-panel-linux-arm64.tar.gz`。
- 节点：`nekopass-agent-linux-amd64`、`nekopass-agent-linux-arm64`。
- 安装和管理脚本：`install-panel.sh`、`install-agent.sh`、`nekopassctl.sh`、`nekopass-update.py`。
- 节点卸载脚本：`uninstall-agent.sh`。

仅上传代码或创建一个没有上述附件的 Release，安装及在线更新仍不能正常使用。`releases/latest/download/…` 需要已经发布的正式 Release；草稿和预发布版本不适合作为此处的默认安装源。

例如，发布成功后可在服务器直接完整卸载所有节点：

```bash
wget https://github.com/Alma0010/nekopass/releases/latest/download/uninstall-agent.sh -O uninstall-agent.sh && sudo bash uninstall-agent.sh
```

指定服务名卸载的用法见 [卸载节点](./node.md#卸载节点)。

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

## 已安装的面板和节点切换下载来源

本次修改更新的是源码中的默认值，已有服务器保存的设置及更新脚本不会因此自动变化。

1. 先发布本 fork 的正式 Release，确认附件可下载。
2. 使用本 fork 的面板安装脚本覆盖升级，保留已有数据；不要添加 `--reinstall`：

   ```bash
   wget https://github.com/Alma0010/nekopass/releases/latest/download/install-panel.sh -O install-panel.sh && sudo bash install-panel.sh
   ```

3. 在面板「系统设置 → 节点安装」将安装脚本地址改为 `https://github.com/Alma0010/nekopass/releases/latest/download/install-agent.sh`，版本下载根地址改为 `https://github.com/Alma0010/nekopass/releases/download`，Agent 版本填写 `latest` 或本 fork 已发布的版本号并保存。
4. 从面板复制最新的节点安装命令，在对应节点重新执行，以安装本 fork 的 Agent 和更新组件。独立命名实例继续使用原有 `--service-name`。

不要只替换服务器上的二进制就认为更新来源也已切换；旧安装留下的更新脚本也需要通过新版安装流程更新。此 fork 的节点组整合需要协议版本 25 的 Agent，面板与节点应使用此 fork 发布的配套版本。

## 常见问题

- **找不到 Run workflow**：先启用 fork 的 Actions，再确认工作流已在默认分支，并使用有仓库写权限的账号。
- **Pages 提示找不到站点或未启用**：在 Settings → Pages 选择 GitHub Actions，再重新运行文档工作流。
- **Actions 失败**：打开失败任务展开红色步骤查看日志。检查、构建或测试失败时不会发布完整版本；不要跳过检查后发布缺少附件的 Release。
- **首次发版前安装地址返回 404**：源码地址和 Release 附件不同；等待正式版本成功发布后再使用 `releases/latest/download/…`。

参考：[GitHub 手动运行工作流](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/manually-run-a-workflow)、[GitHub Pages 发布来源](https://docs.github.com/en/pages/getting-started-with-github-pages/configuring-a-publishing-source-for-your-github-pages-site)、[VitePress 部署](https://vitepress.dev/guide/deploy)。
