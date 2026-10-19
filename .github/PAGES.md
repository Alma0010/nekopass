# 文档站部署

本 fork 文档地址为 https://Alma0010.github.io/nekopass/，部署成功后可访问。

1. 先将修改推送到 `Alma0010/nekopass` 的默认分支 `main`。
2. 打开仓库 **Actions**，若 fork 的工作流尚未启用，先确认启用。
3. 在 **Settings → Pages → Build and deployment → Source** 选择 **GitHub Actions**。
4. 在 **Actions → Deploy documentation → Run workflow** 选择 `main` 并运行。
5. 等待 `build`、`deploy` 成功，访问文档站。

以后默认分支修改 `docs/` 后自动部署，也可手动运行。工作流自动设置仓库子路径或自定义域名的根路径，只上传 `docs/.vitepress/dist/`，无需提交构建产物。

完整的首次启用、Releases 发版、文档预览和已有服务器切换来源步骤见 [发布与文档部署](../docs/guide/publishing.md)。
