# 日常管理

在面板服务器上运行 `sudo nekopassctl` 打开管理菜单。也可以直接运行下面的命令。使用自定义服务名时，把 `panel` 换成安装时指定的完整服务名。

## 查看状态和日志

```bash
sudo nekopassctl panel status
sudo nekopassctl panel logs
sudo nekopassctl panel follow
```

`logs` 查看最近日志，`follow` 持续显示新日志，按 Ctrl+C 退出。节点的对应命令使用 `agent`，例如 `sudo nekopassctl agent logs`。

| 操作 | 命令 |
| --- | --- |
| 启动、停止、重启 | `sudo nekopassctl panel start`、`stop`、`restart` |
| 设置开机自启 | `sudo nekopassctl panel enable` 或 `disable` |
| 修改面板配置 | `sudo nekopassctl panel edit`，保存后运行 `restart` |

## 重置管理员密码

```bash
sudo nekopassctl panel reset-password
```

终端会显示新的随机密码，该管理员需要用新密码重新登录。

## 更新面板

在系统设置的「软件更新」中点击「立即更新」，可在页面查看进度。也可以在服务器上执行下面的命令。安装器会配置在线更新所需的组件，更新时会自动升级数据库并重启服务。

```bash
sudo nekopassctl panel check-update
sudo nekopassctl panel update
```

更新中断时，可重新运行同版本或更新版本的更新命令。更新转发服务器见[升级节点](./node.md#升级)。

## 卸载面板

在面板服务器运行以下命令，按提示输入服务名确认。也可以在 `sudo nekopassctl` 的管理菜单中选择卸载。旧版管理工具没有卸载命令时，请先更新。

```bash
sudo nekopassctl panel uninstall
```

如果同机装了多个面板，用完整服务名指定要卸载的那个，例如 `sudo nekopassctl nekopass-panel-hk1 uninstall`。加 `--dry-run` 可以先查看将执行的操作，加 `--yes` 会跳过确认。

卸载会停止面板和更新服务，移除程序、生成的服务配置和空安装目录。原 `control.env` 会归档到 `/etc/<服务名>/.uninstalled-*/control.env`，实际路径以命令输出为准。归档可能含数据库密码，请妥善保管。

PostgreSQL、面板数据库、证书、运行数据和同机 Agent 会保留。确认本机已经没有其他 Nekopass 服务时，才会移除安装器自带的 `nekopassctl`。手动替换的管理工具会保留。

恢复时，在原 PostgreSQL 集群中用原服务名[重新安装](./panel.md#重新安装)即可沿用原数据库。请省略会清空数据的 `--reinstall`。需要单独移除 Agent 时，见[卸载节点](./node.md#卸载节点)。

## 功能开关

在「系统设置 → 功能开关」中按需关闭下面的功能。四项默认开启，保存后生效。已存的数据和设置会保留。

| 开关 | 关闭后的行为 |
| --- | --- |
| 商用功能 | 隐藏商城、充值、支付设置和订单查询，暂停余额自动续费。已付款的订单仍可通过网关通知入账，管理员仍能分配套餐。 |
| 邀请功能 | 隐藏邀请返利入口，暂停新的邀请绑定和返利。开放注册时无需邀请码。 |
| 工单功能 | 用户和管理员都无法使用工单，重新开启后可继续处理原工单。 |
| 用户端 | 暂停普通用户登录、注册和自助管理。管理员仍可登录后台、管理用户和规则，已有转发照常运行。 |
