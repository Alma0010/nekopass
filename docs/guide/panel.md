# 安装面板

支持 Debian / Ubuntu、amd64 / arm64，使用原生 systemd。

## 本地安装包

将 `install-panel.sh` 与对应架构的安装包放在同一目录：

```bash
sudo bash install-panel.sh --package ./nekopass-panel-linux-amd64.tar.gz
```

ARM64 使用 `nekopass-panel-linux-arm64.tar.gz`。

## 网络安装

从 GitHub Releases 下载：

```bash
wget https://github.com/Alma0010/nekopass/releases/latest/download/install-panel.sh -O install-panel.sh && sudo bash install-panel.sh
```

### 选择 PostgreSQL 集群

安装器会列出本机 `pg_lsclusters` 可识别的 PostgreSQL 14 或以上集群，显示版本、名称、端口和状态。输入编号选择，直接回车优先沿用正在运行的集群。也可以选择新建集群，再选择软件源和数据库主版本。未检测到可用集群时会进入安装流程。

自动化安装可用 `--yes` 接受默认值，也可指定 `--postgres-version` 和 `--db-port` 选中对应集群。如果依赖已齐全，例如本机 PostgreSQL 16 使用端口 5432，可运行以下命令沿用它。

```bash
sudo bash install-panel.sh --host panel.example.com --postgres-version 16 --db-port 5432 --skip-dependencies --yes
```

### 安装完成

安装器在所选集群中创建或复用面板数据库，并配置 systemd 服务。首次创建管理员时，安装摘要会显示一次管理员账号和随机生成的密码。复用已有面板数据库时会保留已有管理员及密码。

| 默认入口 | 端口 |
| --- | --- |
| 网页 HTTP | `8080` |
| 节点控制连接 | `9443` |

打开 `http://服务器地址:8080`。云服务器还需放行安全组端口。

节点连接可选 `plain`（测试明文 HTTP/2）、`existing`（已有公共 CA 证书）或 `proxy`（Nginx 终止 TLS）；默认 `plain`，不生成自签 CA。网页 HTTPS 使用 [反向代理](./proxy.md)。

常用参数：

| 参数 | 用途 |
| --- | --- |
| `--host` | 用户和节点能访问的域名 / IP。 |
| `--http-port` / `--grpc-port` | 网页和节点控制监听端口。 |
| `--agent-host` / `--agent-port` | 节点连接的外部地址 / 端口，适用于代理或 NAT。 |
| `--postgres-source` | 新装 PostgreSQL 时选择 `system`（系统源）或 `pgdg`（官方源）。 |
| `--postgres-version` / `--db-port` | 指定本机 PostgreSQL 主版本和端口，以沿用对应集群。 |
| `--skip-dependencies` | 已满足依赖时跳过软件包安装。 |
| `--admin` / `--site-name` | 初始管理员和站点名称。 |
| `--reinstall` | 清空该服务的面板数据库并全新安装。 |
| `--yes` | 使用参数与默认值，不交互询问。 |
| `--dry-run` | 只验证参数，不安装。 |

全部参数：`bash install-panel.sh --help`。忘记密码见 [日常管理](./manage.md#重置管理员密码)。

## 重新安装

默认以 `control.env` 是否存在判断已有安装；配置不存在时允许重跑，复用同名数据库并保留已有管理员。

清空数据库并全新安装：

```bash
sudo bash install-panel.sh --reinstall
```

**此操作会删除所选 PostgreSQL 实例中该服务的全部面板数据，包括用户、余额、订单、套餐、节点和规则，并重新生成管理员密码。** 执行前自行备份。交互时输入服务名确认；加 `--yes` 会直接执行，不再确认。

只处理 `--service-name` 指定的面板，默认 `nekopass`；同机节点端的配置与状态保留。原节点密钥会随面板数据库清空，节点需在新面板重新添加并配置密钥。
