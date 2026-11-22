# 安装面板

准备一台使用 systemd 的 Debian 或 Ubuntu 服务器，支持 amd64 和 arm64。安装器可以沿用本机的 PostgreSQL，也可以新建集群。

## 网络安装

在面板服务器上运行以下命令，下载并启动安装器。

```bash
wget https://github.com/Alma0010/nekopass/releases/latest/download/install-panel.sh -O install-panel.sh && sudo bash install-panel.sh
```

### 选择 PostgreSQL 集群

安装器会列出本机 `pg_lsclusters` 能识别的 PostgreSQL 14 或以上集群。直接回车优先沿用正在运行的集群，也可以输入编号选择。依赖齐全时会跳过 APT，否则只安装缺少的软件包。

需要单独使用一个集群时，在菜单选择新建。所选主版本已安装时可直接创建集群，需要安装该版本时再选择软件源。找不到可用集群且数据库端口空闲时，安装器会进入新建流程。

例如，可用参数指定端口为 5432 的 PostgreSQL 16 集群。

```bash
sudo bash install-panel.sh --host panel.example.com --postgres-version 16 --db-port 5432 --yes
```

### 沿用 Docker 中的 PostgreSQL

如果 PostgreSQL 14 或以上已通过 Docker 映射到本机，例如 `127.0.0.1:5432`，先准备一个专用空库，或使用原有的 Nekopass 数据库。请使用该库所有者的账号。

当默认端口 `5432`（或 `--db-port` 指定的端口）已被占用且没有可沿用的系统集群时，安装器会转到已有数据库设置。如果出现集群菜单，也可选择「连接已建好的 PostgreSQL」。填写本机端口、数据库名、用户名和密码，密码输入时会隐藏。安装器会检查数据库是否可写，缺少 `psql` 时补装客户端。容器与 PostgreSQL 服务继续由原来的方式管理。

::: details 在容器里创建专用空库

以下示例假设容器和数据库管理员都叫 `postgres`。按实际名称替换后，用管理员账号进入 PostgreSQL。

```bash
docker exec -it postgres psql -U postgres -d postgres
```

```sql
CREATE ROLE nekopass LOGIN;
\password nekopass
CREATE DATABASE nekopass OWNER nekopass;
```

填写 `nekopass` 作为库名和用户名，并在安装时输入刚设置的密码。已有 Nekopass 数据库时，直接填写现有库名和账号。

:::

自动安装时，用 root 创建权限为 `0600` 的 `/root/nekopass-db-url`，在文件中写一行连接地址。将示例中的用户名、密码和数据库名替换为实际值，特殊字符用 URL 编码。

```text
postgres://nekopass:your-url-encoded-password@127.0.0.1:5432/nekopass?sslmode=disable
```

地址支持本机 `127.0.0.1` 或 `[::1]`。如果数据库要求 TLS，使用 `sslmode=require`。

```bash
sudo bash install-panel.sh --host panel.example.com --database-url-file /root/nekopass-db-url --yes
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
| `--postgres-version` / `--db-port` | 指定系统集群的 PostgreSQL 主版本或本机数据库的端口。 |
| `--database-url-file` | 从仅 root 可读的文件读取已有本机 PostgreSQL 的连接地址。 |
| `--skip-dependencies` | 跳过 APT，适用于依赖已齐全的服务器。 |
| `--admin` / `--site-name` | 初始管理员和站点名称。 |
| `--service-name` | 指定面板服务名，默认是 `nekopass`。 |
| `--reinstall` | 清空该服务的面板数据库并全新安装，仅适用于系统集群。 |
| `--yes` | 使用参数与默认值，不交互询问。 |
| `--dry-run` | 只验证参数，不安装。 |

运行 `bash install-panel.sh --help` 查看完整参数。

## 重新安装

已有安装需要升级时，请用[更新命令](./manage.md#更新面板)。[卸载面板](./manage.md#卸载面板)后，可用原服务名和原数据库重新安装，继续使用原来的管理员。Docker 中的原数据库可用上一节的连接方式重新指定。

系统集群中的面板数据库需要清空时，使用 `--reinstall`。

```bash
sudo bash install-panel.sh --reinstall
```

**先备份数据库。** 此操作会删除所选 PostgreSQL 集群中对应面板的用户、余额、订单、套餐、节点和规则，并重新生成管理员密码。节点需要在新面板重新添加并配置密钥。交互安装会要求输入服务名确认，传入 `--yes` 会跳过确认。
