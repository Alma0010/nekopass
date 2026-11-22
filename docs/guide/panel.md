# 安装面板

准备一台使用 systemd 的 Debian 或 Ubuntu 服务器，支持 amd64 和 arm64。安装器可以沿用本机的 PostgreSQL，也可以新建集群。

## 网络安装

在面板服务器上运行以下命令，下载并启动安装器。

```bash
wget https://github.com/Alma0010/nekopass/releases/latest/download/install-panel.sh -O install-panel.sh && sudo bash install-panel.sh
```

### 选择 PostgreSQL 集群

安装器会列出本机 `pg_lsclusters` 能识别的 PostgreSQL 14 或以上集群。直接回车优先沿用正在运行的集群，也可以输入编号选择。依赖齐全时会跳过 APT，否则只安装缺少的软件包。

需要单独使用一个集群时，在菜单选择新建。所选主版本已安装时可直接创建集群，需要安装该版本时再选择软件源。找不到可用集群时，安装器会进入新建流程。

宝塔或 Docker 部署的 PostgreSQL 可能由各自的工具管理。已有服务占用默认或指定的数据库端口，且找不到可沿用的集群时，安装器会提示并停止新建。

例如，可用参数指定端口为 5432 的 PostgreSQL 16 集群。

```bash
sudo bash install-panel.sh --host panel.example.com --postgres-version 16 --db-port 5432 --yes
```

### 登录后台

首次安装完成后，记下终端显示的管理员账号和随机密码。打开 `http://服务器地址:8080` 登录。云服务器上记得在安全组放行所用端口。

复用原面板数据库时，用原管理员账号和密码登录。忘记密码可以在服务器上[重置管理员密码](./manage.md#重置管理员密码)。

| 默认入口 | 端口 |
| --- | --- |
| 网页 HTTP | `8080` |
| 节点控制连接 | `9443` |

默认使用 `plain` 模式，也就是明文 HTTP/2，适合测试。生产环境可选择 `existing` 使用已有的公共 CA 证书，或选择 `proxy` 由 Nginx 处理 TLS。网页 HTTPS 和节点控制连接的示例见 [HTTPS 与反向代理](./proxy.md)。

登录后先[安装节点](./node.md)，等节点上线后再[添加转发规则](./rules.md)。

## 本地安装包

如果已下载安装文件，把 `install-panel.sh` 和对应架构的压缩包放在同一个目录，再运行下面的命令。arm64 服务器使用 `nekopass-panel-linux-arm64.tar.gz`。

```bash
sudo bash install-panel.sh --package ./nekopass-panel-linux-amd64.tar.gz
```

## 常用参数

| 参数 | 用途 |
| --- | --- |
| `--host` | 用户和节点能访问的域名 / IP。 |
| `--http-port` / `--grpc-port` | 网页和节点控制监听端口。 |
| `--agent-host` / `--agent-port` | 节点连接的外部地址 / 端口，适用于代理或 NAT。 |
| `--postgres-source` | 新装 PostgreSQL 时选择 `system`（系统源）或 `pgdg`（官方源）。 |
| `--postgres-version` / `--db-port` | 指定本机 PostgreSQL 主版本和端口，以沿用对应集群。 |
| `--skip-dependencies` | 跳过 APT，适用于依赖已齐全的服务器。 |
| `--admin` / `--site-name` | 初始管理员和站点名称。 |
| `--service-name` | 指定面板服务名，默认是 `nekopass`。 |
| `--reinstall` | 清空该服务的面板数据库并全新安装。 |
| `--yes` | 使用参数与默认值，不交互询问。 |
| `--dry-run` | 只验证参数，不安装。 |

运行 `bash install-panel.sh --help` 查看完整参数。

## 重新安装

已有安装需要升级时，请用[更新命令](./manage.md#更新面板)。[卸载面板](./manage.md#卸载面板)后，可在原 PostgreSQL 集群中用原服务名重新安装，沿用同名数据库和原来的管理员。

只有确定要清空面板数据时才使用 `--reinstall`：

```bash
sudo bash install-panel.sh --reinstall
```

**先备份数据库。** 此操作会删除所选 PostgreSQL 集群中对应面板的用户、余额、订单、套餐、节点和规则，并重新生成管理员密码。节点需要在新面板重新添加并配置密钥。交互安装会要求输入服务名确认，传入 `--yes` 会跳过确认。
