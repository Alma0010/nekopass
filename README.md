# Nekopass

Nekopass 是一个免费的开源 TCP / UDP 转发面板，采用 GPL-3.0 许可证。面板用来管理节点、规则和用户。Agent 安装在节点上，接收面板下发的配置并处理转发。

[下载最新版](https://github.com/Alma0010/nekopass/releases/latest) · [在线文档](https://alma0010.github.io/nekopass/)

## 第一次部署

在 Debian 或 Ubuntu 服务器上安装面板，支持 amd64 和 arm64。

```bash
wget https://github.com/Alma0010/nekopass/releases/latest/download/install-panel.sh -O install-panel.sh && sudo bash install-panel.sh
```

1. 按提示填写面板地址。已有 PostgreSQL 14 或更新版本的本机集群时，直接从菜单选一个。也可以让安装器创建新集群。
2. 保存安装完成时显示的管理员账号和随机密码。网页默认使用 `8080`，Agent 连接面板的控制端口默认使用 `9443`。
3. 登录后到「节点管理」添加节点，把生成的安装命令复制到节点服务器执行。节点上线后，就可以到「转发规则」添加第一条规则。

[面板安装选项](docs/guide/panel.md) · [安装节点](docs/guide/node.md) · [转发规则](docs/guide/rules.md)

## 更新和维护

运行 `sudo nekopassctl` 打开服务菜单。也可以直接执行常用命令。

| 要做的事 | 命令 |
| --- | --- |
| 查看面板状态 | `sudo nekopassctl panel status` |
| 查看面板日志 | `sudo nekopassctl panel logs` |
| 检查面板更新 | `sudo nekopassctl panel check-update` |
| 更新面板 | `sudo nekopassctl panel update` |
| 忘记管理员密码 | `sudo nekopassctl panel reset-password` |

也可以在后台「系统设置 → 软件更新」更新面板，在「节点管理」勾选和筛选节点，通过「批量操作」创建、更新或卸载。更新会保留配置和状态，服务重启时连接会短暂中断。

只需要转发时，可以在「系统设置 → 功能开关」关闭用不到的商用、邀请、工单或用户端。管理员仍可在后台管理用户和规则。各开关的具体行为见 [面板管理](docs/guide/manage.md#功能开关)。

## 卸载

卸载面板可在 `nekopassctl` 菜单中选择，也可以运行以下命令。

```bash
sudo nekopassctl panel uninstall
```

卸载会停止并移除选定的面板及其更新服务，归档面板配置，保留 PostgreSQL、面板数据库和 Agent。停用节点并移走相关规则后，在「节点管理」点「卸载」，面板会让对应的 Agent 卸载自己的服务。离线节点会等到重新连接后处理。已安装的节点需要重新运行新版安装命令来接入自动卸载。详见 [节点卸载说明](docs/guide/node.md)。

## 还可以做什么

- 把 TCP 和 UDP 分别直转或通过隧道中转，也可以让多个 TLS 规则共用端口并按 SNI 分流。见 [转发规则](docs/guide/rules.md) 和 [隧道配置](docs/guide/tunnel.md)。
- 用节点组管理入口和出口，根据需要设置负载均衡、DDNS 和探针。见 [节点管理](docs/guide/nodes.md)。
- 给用户分配套餐、流量和限速。需要收款时，可以启用商城、充值和续费。见 [用户与套餐](docs/guide/users.md) 和 [商城与余额](docs/guide/shop.md)。

## 从源码构建

需要 Go（版本见 `go.mod`）、Node.js 22.12+ 和 Python 3.9+。在仓库根目录运行以下命令。

```bash
npm --prefix web ci
npm --prefix web run build
go build -o dist/bin/nekopass ./cmd/nekopass
go build -o dist/bin/nekopass-agent ./cmd/nekopass-agent
```

前端生成在 `web/dist/`，程序生成在 `dist/bin/`。用 PowerShell 7 运行 `scripts/package-panel.ps1` 可打包面板和 Agent，运行 `scripts/package-agent.ps1` 只打包 Agent。发版和文档部署见 [发布指南](docs/guide/publishing.md)。

## 参与贡献

Fork 后新建分支并提交 PR。一个 PR 尽量只解决一个问题，说明改动和验证结果。使用工具生成代码后，也请先弄明白它做了什么。

```bash
go test ./...
go vet ./...
npm --prefix web test
npm --prefix web run build
python scripts/check-public.py
```

数据库测试使用环境变量 `NEKOPASS_TEST_DATABASE_URL` 指向名称以 `_test` 结尾的独立 PostgreSQL 库。文档改动可用 `npm --prefix docs ci` 和 `npm --prefix docs run build` 验证。

不要提交运行配置、密钥、私钥、数据库、日志或构建产物。

## 鸣谢

Nekopass 的转发面板设计参考了 [nyanpass](https://nyanpass.pages.dev/)，隧道实现参考了 [Xray-core](https://github.com/XTLS/Xray-core)。感谢为代理协议和开源软件做出贡献的开发者。
