# 安装节点

支持 Linux、amd64 / arm64，要求运行中的 systemd。

本页包含 [安装接入](#一键接入)、[升级](#升级) 和 [卸载节点](#卸载节点) 的操作方法。

## 一键接入

在后台「节点管理」创建节点，复制该节点的「安装命令」，以 root 在节点服务器执行。

命令使用 `wget … -O nekopass-install-agent.sh && bash nekopass-install-agent.sh …`，参数直接包含主控 gRPC 地址与端口、节点密钥、版本和下载源。节点地址使用 `https://`（公共 CA 可信证书）或测试用 `http://`（明文 HTTP/2），不附带 CA 参数。命令可重复使用，自动判断首次安装或覆盖安装；传入的连接参数会覆盖配置文件，已有状态文件保留。

节点显示「在线 / 已同步」后即可使用。端口范围、入口出口和探针设置均在后台修改。

## 下载配置

安装文件默认从 GitHub Releases 下载。在「系统设置 → 节点安装」可修改下载配置：

| 设置 | 示例 |
| --- | --- |
| 安装脚本地址 | `https://github.com/Alma0010/nekopass/releases/latest/download/install-agent.sh` |
| 版本下载根地址 | `https://github.com/Alma0010/nekopass/releases/download` |
| Agent 版本 | `latest`（最新正式版，也可填写版本号） |

节点连接信息由安装参数写入 `/etc/nekopass/agent.env`，只需 `NEKOPASS_SERVER` 和 `NEKOPASS_NODE_TOKEN`。规则、限速等运行配置通过 gRPC 下发；节点无需连接网页端口。

## 密钥与连接

节点密钥可在「节点管理 → 编辑」的基本信息中查看、复制和修改，留空保存会生成新密钥。已安装节点更换密钥后，需要更新其连接配置并重启：

```bash
sudo nekopassctl agent configure
sudo nekopassctl agent restart
```

生产环境填写 `https://域名:端口`，证书由公共 CA 签发并覆盖该域名；测试环境填写 `http://IP:端口`。`NEKOPASS_SERVER` 未带协议的旧地址仍按 TLS 连接。

## 升级

在「节点管理」检查更新并点击节点的「更新」，或在节点执行：

```bash
sudo nekopassctl agent check-update
sudo nekopassctl agent update
```

老版本先重新运行新版节点安装命令，即可使用在线更新。

本 fork 合并 v0.19.0 后，入口组与出口组的整合使用 Agent 协议版本 25。使用节点组转发前，请更新为本 fork 构建的 Agent；官方 v0.19.0 与旧自订版的协议版本同为 24，但支持的组功能不同，会暂停不兼容的组规则并提示升级。单节点转发沿用原有兼容检查。

目标 IP 限制使用 v0.15.2 或以上节点；raw(udp) 中转使用 v0.17.1 或以上的面板及节点。

连接配置位于 `/etc/nekopass/agent.env`，运行状态位于 `/var/lib/nekopass-agent/state.db`。新增服务器时，在后台创建对应节点。

## 卸载节点

在需要卸载的节点服务器上执行。默认完整卸载本机全部节点，包括独立命名的节点，以及对应的服务、更新服务、配置、状态数据和专用账户；同机面板和数据库保留。

### 取得脚本

无需克隆仓库。在节点服务器上使用下面的命令，从本 fork 的最新正式 Release 下载脚本后执行。仓库需先发布包含 `uninstall-agent.sh` 的版本；发布步骤见 [发布与文档部署](./publishing.md)。

### 预览卸载范围

只列出将清理的服务和文件，不修改系统：

```bash
wget https://github.com/Alma0010/nekopass/releases/latest/download/uninstall-agent.sh -O uninstall-agent.sh && sudo bash uninstall-agent.sh --dry-run
```

### 完整卸载所有节点（默认）

```bash
wget https://github.com/Alma0010/nekopass/releases/latest/download/uninstall-agent.sh -O uninstall-agent.sh && sudo bash uninstall-agent.sh
```

### 只卸载一个节点

使用安装时指定的服务名。例如，只卸载 `nekopass-agent-hk1`：

```bash
wget https://github.com/Alma0010/nekopass/releases/latest/download/uninstall-agent.sh -O uninstall-agent.sh && sudo bash uninstall-agent.sh --service-name nekopass-agent-hk1
```

服务名也可带 `.service`。预览单个节点的卸载范围时，将两个参数一起使用：

```bash
wget https://github.com/Alma0010/nekopass/releases/latest/download/uninstall-agent.sh -O uninstall-agent.sh && sudo bash uninstall-agent.sh --service-name nekopass-agent-hk1 --dry-run
```

默认节点的服务名是 `nekopass-agent`，只卸载默认节点时同样使用 `--service-name nekopass-agent`。

卸载会删除本地状态和配置备份；面板中的节点记录需按需手动删除。脚本先停止节点和更新服务；更新锁仍被命令行更新占用时退出，等待更新完成后重试。

## 抢占实例重建

同一节点的服务器完全重建后，重新执行原安装命令即可接入。此功能使用 v0.16.1 或以上的面板及节点，每个节点对应一台服务器。

状态文件丢失时，节点使用原密钥获取主控保存的计数并恢复上线。历史已上报流量保留，旧节点尚未结算的有限流量授权按已消耗处理。

重新安装时，传入参数覆盖连接配置。更换密钥或主控地址会将原状态归档为 `state.db.before-reconfigure-*`；切换同一地址的 HTTP / HTTPS 会保留状态。

## 主控暂时离线

节点保留最后下发的配置并持续重连。有限额度用户只能使用该节点已获得的剩余授权，耗尽后等待主控恢复；无限额度用户仍受缓存权限和有效期限制。用量保存在状态文件中，恢复连接后继续上报。

## 动态 IP / DDNS

在节点列表点击「DDNS」，配置 Cloudflare、记录域名、IPv4 / IPv6 和同步参数。完整设置见 [节点与节点组](./nodes.md#ddns)。

## 入口、出口与传输协议

在「节点管理 → 编辑」中，点击「传输协议」旁的「编辑传输协议」，打开子窗口。关闭子窗口会返回节点编辑，保留未保存的基础信息。

入口和出口可同时启用。入口设置是否允许作为入口和是否允许直转。出口分别选择传输协议和安全性：raw(tcp)、h2、raw(udp) 均可搭配明文或 TLS；raw(udp) 仅用于 UDP。uTLS 用于 raw(tcp)、h2 的 TLS 模式。Host、Path、Fallback 和流控仅用于 h2；入口统一使用所选出口的配置。UDP 配置见 [UDP 转发](../protocols/udp.md)。

出口配置始终显示。关闭「作为出口节点」时仍可编辑、保存参数和关联入口，但不开放隧道监听、不启动证书申请；启用后才生效。开关切换不会清空已填配置。

节点编辑保留基本信息、端口范围、连接限制和探针设置。已有节点的协议配置独立保存；添加节点时先在子窗口确定协议，再保存节点即可创建。
