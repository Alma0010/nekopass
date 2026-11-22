#!/usr/bin/env bash
# Native systemd service manager. No application credentials are executed as shell code.
set -uo pipefail

usage() {
 cat <<'HELP'
Nekopass 服务管理（Linux / systemd）

  sudo nekopassctl                         打开交互菜单
  sudo nekopassctl panel                   管理面板端
  sudo nekopassctl agent                   管理节点端
  sudo nekopassctl agent start             启动服务
  sudo nekopassctl panel stop              停止服务
  sudo nekopassctl agent restart           重启服务
  sudo nekopassctl panel status            查看状态
  sudo nekopassctl agent enable            开启开机自启（不立即启动）
  sudo nekopassctl agent disable           关闭开机自启（不停止服务）
  sudo nekopassctl panel logs              最近 100 条日志
  sudo nekopassctl agent follow            实时日志，Ctrl+C 退出
  sudo nekopassctl agent edit              编辑连接配置，保存后手动重启
  sudo nekopassctl agent configure         填写主控 HTTP/HTTPS 地址和节点密钥
  sudo nekopassctl panel edit-service      编辑 systemd 服务覆盖配置
  sudo nekopassctl panel listen-http 127.0.0.1  设置网页监听 IP
  sudo nekopassctl panel listen-grpc 127.0.0.1  设置 Agent 控制监听 IP
  两个监听命令均可填 all，恢复对应端口监听所有地址。
  sudo nekopassctl panel reset-password    重新生成管理员密码，并注销旧会话
  sudo nekopassctl agent check-update      检查 GitHub 最新版本（panel 也可用）
  sudo nekopassctl agent update            更新并重启服务，保留配置和状态
  sudo nekopassctl panel uninstall         卸载面板，保留数据库和运行数据
  sudo nekopassctl panel uninstall --dry-run  预览卸载范围
  sudo nekopassctl panel uninstall --yes      跳过卸载确认

也支持指定独立服务：nekopassctl nekopass-agent-test status、nekopassctl nekopass-panel-test uninstall
这里的启动/停止/重启只作用于服务，不会重启或关闭服务器。
HELP
}
fail() { printf '错误：%s\n' "$*" >&2; return 1; }
select_service() {
 case "$1" in
  panel|control|nekopass) SERVICE=nekopass; CONFIG=/etc/nekopass/control.env; LABEL=面板端;;
  agent|nekopass-agent) SERVICE=nekopass-agent; CONFIG=/etc/nekopass/agent.env; LABEL=节点端;;
  nekopass-panel-*)
   [[ "$1" =~ ^nekopass-panel-[a-z0-9]{1,16}$ ]] || { fail '面板服务名不正确'; return 1; }
   SERVICE=$1; CONFIG="/etc/$SERVICE/control.env"; LABEL="面板端 ($SERVICE)";;
  *)
   [[ "$1" =~ ^nekopass-agent-[a-z0-9]{1,16}$ ]] || { fail '请选择 panel 或 agent'; return 1; }
   SERVICE=$1; CONFIG="/etc/$SERVICE/agent.env"; LABEL="节点端 ($SERVICE)";;
 esac
 [[ "${2:-}" != uninstall ]] || return 0
 [[ "$(systemctl show "$SERVICE.service" -p LoadState --value)" == loaded ]] || {
  fail "未安装服务 $SERVICE.service，请先安装对应服务。"; return 1;
 }
}
reset_password() {
 [[ "$CONFIG" == */control.env ]] || { fail '密码重置仅适用于面板端。'; return 1; }
 config_ready || return
 local username service_user binary
 printf '管理员账号（只有一个管理员时可留空）：'
 read -r username || return 1
 service_user=$(systemctl show "$SERVICE.service" -p User --value)
 [[ "$service_user" =~ ^[a-z_][a-z0-9_-]{0,31}$ ]] || { fail '服务运行账户不正确。'; return 1; }
 binary="/opt/$SERVICE/bin/nekopass"
 [[ -x "$binary" ]] || { fail '面板程序不存在。'; return 1; }
 systemd-run --quiet --wait --pipe --collect -p "User=$service_user" -p "EnvironmentFile=$CONFIG" -- "$binary" -mode reset-admin -admin-name "$username"
}
config_ready() {
 [[ -s "$CONFIG" ]] || { fail "缺少配置 $CONFIG，请先选择编辑配置或填写节点连接信息。"; return 1; }
 if [[ "$CONFIG" == */agent.env ]]; then
  local key
  for key in NEKOPASS_SERVER NEKOPASS_NODE_TOKEN; do
   # Presence check only: never source an EnvironmentFile.
   awk -v key="$key" 'index($0,key "=")==1 { v=substr($0,length(key)+2); gsub(/^[[:space:]"\047]+|[[:space:]"\047]+$/, "", v); if(length(v)) ok=1 } END {exit !ok}' "$CONFIG" || {
    fail "$CONFIG 中的 $key 尚未填写。"; return 1;
   }
  done
 fi
}
backup_config() {
 [[ ! -f "$CONFIG" ]] || cp -p -- "$CONFIG" "$CONFIG.bak.$(date +%Y%m%d-%H%M%S).$$"
}
prepare_config() {
 [[ ! -L "$CONFIG" ]] || { fail '配置文件不能是符号链接。'; return 1; }
 install -d -m 755 "$(dirname "$CONFIG")" || return
 backup_config || return
 if [[ ! -f "$CONFIG" ]]; then
  [[ "$CONFIG" != */control.env ]] || { fail '面板配置缺失，请恢复数据库连接配置。'; return 1; }
  (umask 077; printf 'NEKOPASS_SERVER=\nNEKOPASS_NODE_TOKEN=\n' > "$CONFIG") || return
 fi
 chmod 600 "$CONFIG"
}
edit_config() {
 local -a editor_cmd
 if [[ -n "${EDITOR:-}" ]]; then
  read -r -a editor_cmd <<< "$EDITOR"
 elif command -v nano >/dev/null; then editor_cmd=(nano)
 elif command -v vi >/dev/null; then editor_cmd=(vi)
 else fail '请安装 nano/vi，或设置 EDITOR。'; return 1; fi
 command -v "${editor_cmd[0]}" >/dev/null || { fail '找不到 EDITOR 指定的编辑器。'; return 1; }
 prepare_config || return
 "${editor_cmd[@]}" "$CONFIG" || return
 chmod 600 "$CONFIG" || return
 printf '配置已保存。执行 nekopassctl %s restart 后生效。\n' "$SERVICE"
}
configure_agent() {
 [[ "$CONFIG" != */control.env ]] || { fail '此功能用于节点端，面板端请选择编辑配置。'; return 1; }
 local server token
 printf '主控地址（https://panel.example.com:9443 或测试用 http://IP:9443）：'
 read -r server || return 1
 python3 - "$server" <<'PY'
import sys,urllib.parse
raw=sys.argv[1];u=urllib.parse.urlsplit(raw if '://' in raw else '//'+raw)
try:port=u.port
except ValueError:sys.exit('主控端口无效')
if u.scheme not in ('','http','https') or not u.hostname or not port or not 1<=port<=65535 or u.path not in ('','/') or u.query or u.fragment or u.username or u.password or any(c in raw for c in '\r\n\0'):sys.exit('主控地址格式无效')
PY
 [[ $? -eq 0 ]] || return 1
 printf '节点密钥（从后台节点编辑中复制，输入不回显）：'
 read -rs token || return 1
 printf '\n'
 [[ "$token" =~ ^[A-Za-z0-9_-]{8,128}$ ]] || { fail '节点密钥格式不正确。'; return 1; }
 # Preserve other entries, including optional Agent settings. Do not touch state.db.
 prepare_config || return
 local tmp
 tmp=$(mktemp "$(dirname "$CONFIG")/.agent.env.XXXXXXXX") || return
 if ! {
  awk '!/^[[:space:]]*(NEKOPASS_SERVER|NEKOPASS_NODE_TOKEN|NEKOPASS_CA)=/' "$CONFIG" &&
  printf '\nNEKOPASS_SERVER=%s\nNEKOPASS_NODE_TOKEN=%s\n' "$server" "$token"
 } > "$tmp"; then rm -f -- "$tmp"; return 1; fi
 chmod 600 "$tmp" && mv -f -- "$tmp" "$CONFIG" || return
 printf '连接配置已保存。请确认密钥属于此机器原来的节点；状态文件和节点身份均保留。\n'
 printf '执行 nekopassctl %s restart 启动并应用配置。\n' "$SERVICE"
}
panel_maintenance() {
 [[ "$CONFIG" == */control.env ]] || { fail '此命令仅适用于面板端。'; return 1; }
 python3 - "$SERVICE" "$@" <<'NEKOPASS_PANEL_UNINSTALL_PYTHON'
import argparse
import contextlib
import errno
import fcntl
import http.client
import ipaddress
import json
import os
import pathlib
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import time


def safe_path(path, root, *, directory=False, state_file=False):
    """Reject symlink traversal and writes under untrusted parent directories."""
    current = root
    parts = path.relative_to(root).parts
    for index, part in enumerate(parts):
        current /= part
        last = index == len(parts) - 1
        try:
            info = current.lstat()
        except FileNotFoundError:
            return False
        is_directory = not last or directory
        # The application owns its state directory and writes the two update
        # signals. Its parent (/var/lib) still prevents replacing the directory.
        app_state = state_file and index >= len(parts) - 2
        # Some systems allow creating files in /run/lock. Sticky bit and the
        # descriptor checks in update_lock protect the lock file we open there.
        sticky_lock_dir = current == root / 'run/lock' and info.st_mode & stat.S_ISVTX
        if ((is_directory and not stat.S_ISDIR(info.st_mode))
                or (not is_directory and not stat.S_ISREG(info.st_mode))
                or (not app_state and (info.st_uid != 0
                    or info.st_mode & 0o022 and not sticky_lock_dir))):
            raise ValueError(f'路径类型、归属或权限不符合安装布局：{current}')
    return True


def unit_info(path):
    result = subprocess.run(
        ['systemctl', 'show', '--all', '--no-pager',
         '-p', 'LoadState,ActiveState,MainPID,FragmentPath,ExecStart,EnvironmentFiles,User,DropInPaths,Paths,Unit,NeedDaemonReload',
         path.name], text=True, capture_output=True, timeout=10,
    )
    info = {}
    for line in result.stdout.splitlines():
        key, equals, value = line.partition('=')
        if equals:
            info[key] = info[key] + '\n' + value if key in info else value
    if result.returncode and info.get('LoadState') != 'not-found':
        raise ValueError(f'无法读取服务状态：{path.name}')
    return info


def installed_unit(path, root, checks):
    exists = safe_path(path, root)
    info = unit_info(path)
    state = info.get('LoadState')
    if not exists and state == 'not-found':
        return False
    verified = {'LoadState': state == 'loaded', 'FragmentPath': info.get('FragmentPath') == str(path)}
    if exists:
        verified.update(checks(info))
    else:
        verified['ActiveState'] = info.get('ActiveState') in ('inactive', 'failed')
    mismatch = [field for field, passed in verified.items() if not passed]
    if mismatch:
        raise ValueError(f'无法确认服务属于所选面板：{path.name}（{", ".join(mismatch)}）')
    # A retry can encounter an inactive unit still cached by the systemd manager.
    return exists


def starts_with(info, args, *, exact=False):
    starts = re.findall(r'\bpath=([^ ;]+) ; argv\[\]=([^;]*?) ;', info.get('ExecStart', ''))
    if len(starts) != 1 or starts[0][0] != args[0]:
        return False
    argv = starts[0][1].split()
    return (argv if exact else argv[:len(args)]) == args


@contextlib.contextmanager
def update_lock(path, root):
    safe_path(path, root)
    flags = os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_NONBLOCK
    with os.fdopen(os.open(path, flags, 0o600)) as lock:
        info = os.fstat(lock.fileno())
        if (not stat.S_ISREG(info.st_mode) or info.st_uid != 0
                or info.st_nlink != 1 or info.st_mode & 0o022):
            raise ValueError('更新锁不符合安装布局。')
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise ValueError('面板正在更新或执行其他管理操作，请稍后再试。') from None
        yield


def confirm(service):
    try:
        with open('/dev/tty', 'r', encoding='utf-8') as response:
            with open('/dev/tty', 'w', encoding='utf-8') as terminal:
                terminal.write(f'请输入服务名 {service} 确认卸载：')
            return response.readline().strip() == service
    except OSError:
        raise ValueError('需要终端确认。自动化调用请显式添加 --yes。') from None


def check_web_mounts(web):
    # A nested mount, including a bind mount on the same filesystem, may
    # contain unrelated data. Do not recursively remove any such tree.
    for entry in pathlib.Path('/proc/self/mountinfo').read_text().splitlines():
        mount = pathlib.Path(re.sub(r'\\([0-7]{3})', lambda m: chr(int(m[1], 8)), entry.split()[4]))
        if mount == web or web in mount.parents:
            raise ValueError(f'网站目录内存在挂载点，请先卸载：{mount}')


def uses_shared_updater(base, config, state, units):
    if (any((base / 'bin').glob('nekopass-agent*'))
            or any(config.parent.glob('agent.env*'))
            or (state / 'state.db').exists() or (state / 'state.db').is_symlink()):
        return True
    if base.name != 'nekopass':
        return False
    for suffix in ('.service', '-update.service', '-update.path', '-uninstall.service', '-uninstall.path'):
        unit = units / ('nekopass-agent' + suffix)
        if unit.exists() or unit.is_symlink():
            return True
        try:
            if unit_info(unit).get('LoadState') != 'not-found':
                return True
        except ValueError:
            # An unavailable service query cannot prove the helper is unused.
            return True
    return False


@contextlib.contextmanager
def directory_lock(directory):
    if os.name != 'posix':
        yield
        return
    import fcntl
    descriptor = os.open(directory, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        fcntl.flock(descriptor, fcntl.LOCK_EX)
        yield
    finally:
        os.close(descriptor)


def manager_needed(root, ignored_units):
    patterns = ('nekopass*.service', 'nekopass*.path')
    for kind, flags, ignored in (
        ('list-unit-files', (), ()),
        ('list-units', ('--plain', '--state=active,activating,deactivating,reloading'), ignored_units),
    ):
        result = subprocess.run(['systemctl', kind, '--no-legend', '--no-pager', *flags, *patterns],
                                capture_output=True, text=True, timeout=5)
        if result.returncode:
            return True
        for line in result.stdout.splitlines():
            fields = line.split()
            if fields and fields[0].endswith(('.service', '.path')) and fields[0] not in ignored:
                return True
    for home, files in (
        (root / 'etc', ('control.env', 'agent.env')),
        (root / 'opt', ('bin/nekopass', 'bin/nekopass-agent')),
    ):
        # Archives and empty installation directories do not need the manager.
        for directory in home.glob('nekopass*'):
            for name in files:
                marker = directory / name
                if marker.exists() or marker.is_symlink():
                    return True
    return False


def cleanup_manager(root, ignored_units=(), dry_run=False):
    manager = root / 'usr/local/bin/nekopassctl'
    try:
        if not manager.is_file() or manager.is_symlink():
            return
        owner = root.stat().st_uid
        for parent in manager.parents:
            if parent == root:
                break
            info = parent.lstat()
            if not stat.S_ISDIR(info.st_mode) or info.st_uid != owner or (os.name == 'posix' and info.st_mode & 0o022):
                return
        with directory_lock(manager.parent):
            info = manager.lstat()
            if not stat.S_ISREG(info.st_mode) or info.st_uid != owner or info.st_nlink != 1 or (os.name == 'posix' and info.st_mode & 0o022):
                return
            with manager.open('rb') as file:
                if (file.readline(128) != b'#!/usr/bin/env bash\n'
                        or file.readline(128) != b'# Native systemd service manager. No application credentials are executed as shell code.\n'):
                    return
            if manager_needed(root, ignored_units):
                return
            if not dry_run:
                manager.unlink()
            print(f'{"将移除" if dry_run else "已移除"}闲置的服务管理脚本：{manager}')
    except (OSError, subprocess.TimeoutExpired):
        # Cleanup cannot be proven safe. Keep the shared script for a later retry.
        print('未能确认共用管理脚本已闲置，已保留。', file=sys.stderr)



def verified_archive(config, root):
    if not safe_path(config.parent, root, directory=True):
        return False
    for directory in config.parent.glob('.uninstalled-*'):
        archived = directory / config.name
        try:
            if (safe_path(directory, root, directory=True)
                    and stat.S_IMODE(directory.stat().st_mode) == 0o700
                    and safe_path(archived, root)
                    and stat.S_IMODE(archived.stat().st_mode) == 0o600):
                return True
        except ValueError:
            continue
    return False


def packaged_updater(path, root):
    try:
        if not safe_path(path, root) or path.stat().st_nlink != 1:
            return False
        with path.open('rb') as file:
            return (file.readline(128) == b'#!/usr/bin/python3\n'
                    and file.readline(128) == b'"""Native service updates from Alma0010/nekopass GitHub Releases."""\n')
    except (OSError, ValueError):
        return False


def empty_directories(candidates, remove, root):
    planned = set(remove)
    empty = []
    for path in candidates:
        if safe_path(path, root, directory=True) and all(child in planned for child in path.iterdir()):
            empty.append(path)
            planned.add(path)
    return empty


def remove_empty_directories(directories):
    for path in directories:
        try:
            path.rmdir()
            print(f'已移除空目录：{path}')
        except OSError as error:
            if error.errno not in (errno.ENOENT, errno.ENOTEMPTY, errno.EEXIST, errno.EBUSY):
                raise


def uninstall(service, dry_run, yes, root=pathlib.Path('/'), *, locked=False):
    if not re.fullmatch(r'nekopass(?:-panel-[a-z0-9]{1,16})?', service):
        raise ValueError('面板服务名不正确。')
    units = root / 'etc/systemd/system'
    primary = units / f'{service}.service'
    watcher = units / f'{service}-update.path'
    updater = units / f'{service}-update.service'
    dropin = units / f'{service}.service.d/update.conf'
    config = root / 'etc' / service / 'control.env'
    base = root / 'opt' / service
    binary = base / 'bin/nekopass'
    update_helper = base / 'bin/nekopass-update'
    web = base / 'web'
    state = root / 'var/lib' / service
    request = state / 'update-request.json'
    ready = state / 'panel-update-ready'
    manager = root / 'usr/local/bin/nekopassctl'
    alias = 'panel' if service == 'nekopass' else service

    has_main = installed_unit(primary, root, lambda info: {
        'User': info.get('User') == service,
        'EnvironmentFiles': str(config) in info.get('EnvironmentFiles', '').split(),
        'ExecStart': starts_with(info, [str(binary), '-web', str(web)]),
    })
    has_watcher = installed_unit(watcher, root, lambda info: {
        'NeedDaemonReload': info.get('NeedDaemonReload') == 'no',
        'DropInPaths': not info.get('DropInPaths'),
        'Paths': info.get('Paths') == f'{request} (PathExists)',
        'Unit': info.get('Unit') == updater.name,
    })
    has_updater = installed_unit(updater, root, lambda info: {
        'DropInPaths': not info.get('DropInPaths'),
        'ExecStart': starts_with(info, [str(manager), alias, 'update'], exact=True),
    })
    files = [binary, binary.with_suffix('.previous'), binary.with_suffix('.next'), binary.with_suffix('.update-pending')]
    # A verified updater unit identifies the default panel's shared helper.
    if (has_main and (service != 'nekopass' or has_updater)
            and not uses_shared_updater(base, config, state, units)):
        files.append(update_helper)
    remove = [path for path in files if safe_path(path, root)]
    generated_dropin = f'[Service]\nStateDirectory={service}\nEnvironment=NEKOPASS_PANEL_UPDATE_DIR={state}\n'
    if safe_path(dropin, root) and dropin.read_text(encoding='utf-8') == generated_dropin:
        remove.append(dropin)
    remove.extend(path for path in (request, ready) if safe_path(path, root, state_file=True))
    has_web = safe_path(web, root, directory=True)
    if has_web:
        check_web_mounts(web)
    has_config = safe_path(config, root)
    if has_config and config.stat().st_nlink != 1:
        raise ValueError(f'面板配置不能是硬链接：{config}')
    if not has_main and (has_watcher or has_updater or remove or has_web or has_config):
        raise ValueError(f'找不到可验证的 {primary.name}，无法安全判断哪些文件属于面板。')

    manager_cleanup = has_main or verified_archive(config, root)
    if (not has_main and manager_cleanup and packaged_updater(update_helper, root)
            and not uses_shared_updater(base, config, state, units)):
        remove.append(update_helper)
    directories = empty_directories((dropin.parent, binary.parent, base), [*remove, *([web] if has_web else [])], root)
    reload_needed = has_main or unit_info(primary).get('LoadState') == 'loaded'
    if not (reload_needed or directories or remove or (manager_cleanup and manager.exists())):
        print(f'面板已卸载：{service}')
        return 0

    installed = [path for path, present in ((watcher, has_watcher), (updater, has_updater), (primary, has_main)) if present]
    if not locked:
        print(f'面板：{service}')
        if installed:
            print('将停用并移除服务：' + ' '.join(path.name for path in installed))
        for path in remove:
            print(f'将移除：{path}')
        if has_web:
            print(f'将移除：{web}')
        if has_config:
            print(f'将归档面板配置至 {config.parent}/.uninstalled-*/control.env')
        for path in directories:
            print(f'将移除空目录：{path}')
        print('保留 PostgreSQL、数据库、运行数据与节点端。')
        if dry_run:
            if has_main:
                print('最后一个 Nekopass 实例卸载后会移除管理命令。')
            elif manager_cleanup:
                cleanup_manager(root, dry_run=True)
            return 0
        if not yes and not confirm(service):
            print('已取消卸载。')
            return 1

        # Recheck the layout after confirmation with updates and other
        # uninstallers excluded. No lock file is created for a preview.
        with update_lock(root / 'run/lock' / f'{service}-update.lock', root):
            return uninstall(service, False, True, root, locked=True)

    for path in installed:
        subprocess.run(['systemctl', 'stop', path.name], check=True)
    if installed:
        subprocess.run(['systemctl', 'disable', *[path.name for path in installed if path != updater]], check=True)
    if has_config:
        archive = pathlib.Path(tempfile.mkdtemp(prefix='.uninstalled-', dir=config.parent)) / config.name
        config.chmod(0o600)
        config.rename(archive)
        print(f'面板配置已归档：{archive}')
    if has_web:
        shutil.rmtree(web)
    for path in remove:
        if path == update_helper:
            with directory_lock(path.parent):
                if (has_main or packaged_updater(path, root)) and not uses_shared_updater(base, config, state, units):
                    path.unlink(missing_ok=True)
        else:
            path.unlink(missing_ok=True)
    # Keep the main unit until last so an interrupted removal is retryable.
    for path in installed:
        path.unlink(missing_ok=True)
    remove_empty_directories(directories)
    if reload_needed:
        subprocess.run(['systemctl', 'daemon-reload'], check=True)
    if manager_cleanup:
        cleanup_manager(root)
    print(f'面板已卸载：{service}')
    return 0



def atomic_write_unit(unit, data):
    info = unit.stat()
    if info.st_nlink != 1:
        raise ValueError('不能覆盖带硬链接的服务单元。')
    descriptor, name = tempfile.mkstemp(prefix='.' + unit.name + '-', dir=unit.parent)
    temporary = pathlib.Path(name)
    try:
        with os.fdopen(descriptor, 'wb') as target:
            if os.name == 'posix':
                os.fchown(target.fileno(), info.st_uid, info.st_gid)
            temporary.chmod(stat.S_IMODE(info.st_mode))
            target.write(data)
            target.flush()
            os.fsync(target.fileno())
        temporary.replace(unit)
    finally:
        temporary.unlink(missing_ok=True)


def process_listens_on(pid, host, port):
    proc = pathlib.Path('/proc') / pid
    sockets = set()
    for descriptor in (proc / 'fd').iterdir():
        try:
            target = str(descriptor.readlink())
        except FileNotFoundError:
            continue
        if target.startswith('socket:[') and target.endswith(']'):
            sockets.add(target[8:-1])
    requested = ipaddress.ip_address(host) if host else None
    requested = getattr(requested, 'ipv4_mapped', None) or requested
    found = False
    for kind in ('tcp', 'tcp6'):
        try:
            with (proc / 'net' / kind).open(encoding='ascii') as table:
                next(table, None)
                for line in table:
                    fields = line.split()
                    # 0A is TCP_LISTEN. Inode ownership identifies this service.
                    if len(fields) < 10 or fields[3] != '0A' or fields[9] not in sockets:
                        continue
                    encoded, listen_port = fields[1].split(':')
                    if int(listen_port, 16) != port:
                        continue
                    packed = b''.join(int(encoded[i:i+8], 16).to_bytes(4, sys.byteorder)
                                      for i in range(0, len(encoded), 8))
                    address = ipaddress.ip_address(packed)
                    address = getattr(address, 'ipv4_mapped', None) or address
                    matches = address.is_unspecified if requested is None or requested.is_unspecified else address == requested
                    if not matches:
                        return False
                    found = True
        except FileNotFoundError:
            continue
    return found


def wait_panel_ready(unit, host, port, web):
    web_host, web_port = web
    try:
        wildcard = not web_host or ipaddress.ip_address(web_host).is_unspecified
    except ValueError:
        wildcard = False
    if wildcard:
        hosts = ('127.0.0.1', '::1')
    else:
        hosts = (web_host,)
    deadline = time.monotonic() + 15
    while time.monotonic() < deadline:
        info = unit_info(unit)
        pid = info.get('MainPID', '')
        if info.get('ActiveState') == 'active' and pid.isascii() and pid.isdigit() and int(pid) > 0:
            try:
                listening = process_listens_on(pid, host, port)
            except FileNotFoundError:
                listening = False
            if listening:
                for probe in hosts:
                    connection = http.client.HTTPConnection(probe, web_port, timeout=1)
                    try:
                        connection.request('GET', '/healthz')
                        response = connection.getresponse()
                        if response.status == 200 and json.loads(response.read(256)) == {'status': 'ok'}:
                            return True
                    except (OSError, http.client.HTTPException, ValueError):
                        pass
                    finally:
                        connection.close()
        time.sleep(.25)
    return False


def read_agent_transport(service, root, info):
    config = root / 'etc' / service / 'control.env'
    unknown = '无法核对 Agent 连接方式。请检查原生安装的数据库配置与 PostgreSQL 服务。'
    if (info.get('User') != service or info.get('EnvironmentFiles') != f'{config} (ignore_errors=no)'
            or not safe_path(config, root)):
        raise ValueError(unknown)
    # Read only the installer-generated peer-authenticated endpoint. Never
    # source the environment file or pass a connection secret in argv.
    contents = config.read_text('utf-8')
    # EnvironmentFile may allow leading blanks and continuations. Do not
    # authorize a loopback bind from an ambiguous local database assignment.
    if '\\' + '\n' in contents:
        raise ValueError(unknown)
    values = re.findall(r'(?m)^[ \t]*NEKOPASS_DATABASE_URL[ \t]*=[ \t]*([^\r\n]*)$', contents)
    native_uri = re.escape(f'postgres://{service}@/{service}?host=/var/run/postgresql&port=') + r'([0-9]{1,5})&sslmode=disable'
    match = re.fullmatch(native_uri, values[0]) if len(values) == 1 else None
    if not match or not 1 <= int(match[1]) <= 65535:
        raise ValueError(unknown)
    query = "SELECT COALESCE(NULLIF(config->>'agent_transport',''),'tls') FROM site_settings WHERE id=1"
    env = {key: value for key, value in os.environ.items() if not key.startswith('PG')}
    try:
        result = subprocess.run(['runuser', '-u', service, '--', 'psql', '-w', '-XAtq',
                                 '-h', '/var/run/postgresql', '-p', match[1],
                                 '-U', service, '-d', service, '-c', query],
                                text=True, capture_output=True, cwd='/', env=env, timeout=8)
    except (OSError, subprocess.SubprocessError):
        raise ValueError(unknown) from None
    mode = result.stdout.strip()
    if result.returncode or mode not in ('plain', 'tls'):
        raise ValueError(unknown)
    return mode


def listen_address(host, port):
    return f'[{host}]:{port}' if ':' in host else f'{host}:{port}'


def listen_option(arguments, flag):
    options = list(re.finditer(rf'(?<!\S)--?{flag}(?:=|[ \t]+)(?P<listen>\S+)', arguments))
    if len(options) != 1:
        raise ValueError(f'启动命令必须包含一个可识别的 -{flag} 参数。')
    endpoint = re.fullmatch(r'(?:\[([^\]\s]+)\]|([^:\s]*)):([0-9]{1,5})', options[0]['listen'])
    if not endpoint or not 1 <= int(endpoint[3]) <= 65535:
        raise ValueError(f'无法识别当前 -{flag} 参数中的监听端口。')
    return options[0], (endpoint[1] or endpoint[2] or '', int(endpoint[3]))


def listen_plan(service, listener, host, root, unit):
    info = unit_info(unit)
    if (not safe_path(unit, root) or info.get('LoadState') != 'loaded'
            or info.get('FragmentPath') != str(unit) or info.get('NeedDaemonReload') == 'yes'):
        raise ValueError('无法核对面板服务单元，请检查配置并重新加载 systemd。')
    active = info.get('ActiveState')
    if active not in ('active', 'inactive', 'failed'):
        raise ValueError('服务正在切换状态，请稍后再试。')
    previous = unit.read_bytes()
    text = previous.decode('utf-8')
    starts = list(re.finditer(r'(?m)^ExecStart=(?P<arguments>[^\r\n]+)\r?$', text))
    if len(starts) != 1:
        raise ValueError('无法识别启动命令，请用 edit-service 调整自定义配置。')
    arguments = starts[0]['arguments']
    executable = (root / 'opt' / service / 'bin/nekopass').as_posix()
    argv = arguments.split()
    if (not argv or argv[0] != executable or any(c in arguments for c in "\"'\\;$%")
            or not starts_with(info, argv, exact=True)):
        raise ValueError('发现自定义或未加载的启动命令，请用 edit-service 调整。')
    option, (_, port) = listen_option(arguments, listener)
    web = (host, port) if listener == 'http' else listen_option(arguments, 'http')[1]
    left, right = option.span('listen')
    changed_arguments = arguments[:left] + listen_address(host, port) + arguments[right:]
    left, right = starts[0].span('arguments')
    changed = (text[:left] + changed_arguments + text[right:]).encode('utf-8')
    return previous, changed, port, web, info, changed_arguments.split()


def run_panel_systemctl(*args):
    subprocess.run(['systemctl', *args], check=True, timeout=45)


def set_listen_ip(service, listener, address, root=pathlib.Path('/')):
    if not re.fullmatch(r'nekopass(?:-panel-[a-z0-9]{1,16})?', service):
        raise ValueError('面板服务名不正确。')
    if address == 'all':
        host = ''
    else:
        if '%' in address:
            raise ValueError('请输入不带端口和区域后缀的 IP 地址，或填写 all。')
        try:
            host = str(ipaddress.ip_address(address))
        except ValueError:
            raise ValueError('请输入 IPv4、IPv6 地址，或填写 all。') from None
    unit = root / 'etc/systemd/system' / (service + '.service')
    with update_lock(root / 'run/lock' / (service + '-update.lock'), root):
        previous, changed, port, web, info, argv = listen_plan(service, listener, host, root, unit)
        requested = ipaddress.ip_address(host) if host else None
        requested = getattr(requested, 'ipv4_mapped', None) or requested
        loopback = requested is not None and requested.is_loopback
        if listener == 'grpc' and loopback and read_agent_transport(service, root, info) != 'tls':
            raise ValueError('请先在面板中选择 HTTPS 节点连接方式。HTTP 直连模式会将回环控制端口扩大为全地址监听。')
        running = info['ActiveState'] == 'active'
        label = '网页' if listener == 'http' else 'Agent 控制'
        endpoint = listen_address(host, port)
        if changed == previous:
            if running and not wait_panel_ready(unit, host, port, web):
                raise ValueError('当前监听与配置不符，请检查面板状态。')
            print(f'{label}已设置为监听 {endpoint}。')
            return 0
        atomic_write_unit(unit, changed)
        restart_attempted = False
        try:
            run_panel_systemctl('daemon-reload')
            if not starts_with(unit_info(unit), argv, exact=True):
                raise ValueError('systemd 未采用新的启动命令，请检查 ExecStart 覆盖配置。')
            if running:
                restart_attempted = True
                run_panel_systemctl('restart', unit.name)
                if not wait_panel_ready(unit, host, port, web):
                    raise ValueError(f'{label}监听或网页健康检查失败。')
        except (OSError, ValueError, subprocess.SubprocessError, KeyboardInterrupt) as error:
            if unit.read_bytes() != changed:
                raise ValueError('服务单元已有其他更改，请核对当前配置。') from None
            try:
                atomic_write_unit(unit, previous)
                run_panel_systemctl('daemon-reload')
                if restart_attempted:
                    run_panel_systemctl('restart', unit.name)
            except (OSError, ValueError, subprocess.SubprocessError):
                raise ValueError('设置失败，未能完整恢复服务，请检查 systemd 状态。') from None
            detail = str(error) if isinstance(error, ValueError) else '无法完成服务配置或重启。'
            raise ValueError(f'{detail} 已还原原来的监听配置。') from None
    if running:
        print(f'{label}正在监听 {endpoint}。')
    else:
        print(f'{label}已设置为监听 {endpoint}，下次启动时生效。')
    return 0


parser = argparse.ArgumentParser(description='维护面板 systemd 服务', allow_abbrev=False)
parser.add_argument('service')
actions = parser.add_subparsers(dest='action', required=True)
remove_panel = actions.add_parser('uninstall', help='卸载面板并保留数据库和运行数据', allow_abbrev=False)
flags = remove_panel.add_mutually_exclusive_group()
flags.add_argument('--dry-run', action='store_true', help='仅预览')
flags.add_argument('--yes', action='store_true', help='跳过交互确认')
listen_http = actions.add_parser('listen-http', help='设置网页监听 IP', allow_abbrev=False)
listen_http.add_argument('address', metavar='IP|all')
listen_http.set_defaults(listener='http')
listen_grpc = actions.add_parser('listen-grpc', help='设置 Agent 控制监听 IP', allow_abbrev=False)
listen_grpc.add_argument('address', metavar='IP|all')
listen_grpc.set_defaults(listener='grpc')
args = parser.parse_args()
try:
    if args.action == 'uninstall':
        sys.exit(uninstall(args.service, args.dry_run, args.yes))
    sys.exit(set_listen_ip(args.service, args.listener, args.address))
except (OSError, ValueError, subprocess.SubprocessError) as error:
    operation = '卸载中止' if args.action == 'uninstall' else '设置监听 IP 失败'
    sys.exit(f'{operation}：{error}')

NEKOPASS_PANEL_UNINSTALL_PYTHON
}
run_action() {
 case "$1" in
  start|restart)
   config_ready || return
   systemctl "$1" "$SERVICE.service" || return
   systemctl --no-pager --full status "$SERVICE.service";;
  stop) systemctl stop "$SERVICE.service";;
  status) systemctl --no-pager --full status "$SERVICE.service";;
  enable|disable) systemctl "$1" "$SERVICE.service";;
  logs) journalctl -u "$SERVICE.service" -n 100 --no-pager;;
  follow) journalctl -u "$SERVICE.service" -n 50 -f;;
  edit) edit_config;;
  configure) configure_agent;;
  reset-password) reset_password;;
  edit-service) systemctl edit "$SERVICE.service" && systemctl daemon-reload;;
  uninstall) panel_maintenance "$@";;
  listen-http|listen-grpc)
   if (($# == 1)); then
    [[ "$CONFIG" == */control.env ]] || { fail '此命令仅适用于面板端。'; return 1; }
    local address listener=网页
    [[ "$1" != listen-grpc ]] || listener='Agent 控制'
    printf '%s监听 IP（默认 127.0.0.1，all 监听所有地址）：' "$listener"
    read -r address || return 1
    set -- "$1" "${address:-127.0.0.1}"
   fi
   panel_maintenance "$@";;
  check-update|update)
   local base updater
   case "$SERVICE" in nekopass|nekopass-agent) base=/opt/nekopass;; *) base="/opt/$SERVICE";; esac
   updater="$base/bin/nekopass-update"
   [[ -x "$updater" ]] || { fail '请先使用新版安装脚本安装更新服务。'; return 1; }
   if [[ "$1" == check-update ]]; then
    "$updater" --service "$SERVICE" --check
   elif [[ "$SERVICE" != nekopass-agent* && -e "/var/lib/$SERVICE/update-request.json" ]]; then
    # Panel and command-line updates share the same command. A queued panel
    # request supplies the pinned version and receives durable progress/result.
    "$updater" --service "$SERVICE" --request
   else
    "$updater" --service "$SERVICE"
   fi;;
  *) fail "未知操作：$1"; usage; return 1;;
 esac
}
service_menu() {
 local choice action
 while true; do
  printf '\nNekopass · %s\n' "$LABEL"
  printf '运行状态：%s  开机自启：%s\n配置：%s\n' "$(systemctl is-active "$SERVICE.service" 2>/dev/null)" "$(systemctl is-enabled "$SERVICE.service" 2>/dev/null)" "$CONFIG"
  printf '1 启动  2 停止  3 重启  4 查看状态\n5 开启自启  6 关闭自启  7 最近日志  8 实时日志\n9 编辑配置  10 填写节点连接信息  11 编辑服务配置\n'
  [[ "$CONFIG" != */control.env ]] || printf '12 重新生成管理员密码\n'
  printf '13 检查更新  14 更新到最新版本\n'
  [[ "$CONFIG" != */control.env ]] || printf '15 卸载面板  16 设置网页监听 IP  17 设置 Agent 控制监听 IP\n'
  printf '0 返回/退出\n请选择：'
  read -r choice || return 0
  case "$choice" in
   0) return 0;; 1) action=start;; 2) action=stop;; 3) action=restart;; 4) action=status;;
   5) action=enable;; 6) action=disable;; 7) action=logs;; 8) action=follow;;
   9) action=edit;; 10) action=configure;; 11) action=edit-service;;
   12) action=reset-password;;
   13) action=check-update;; 14) action=update;;
   15) action=uninstall;; 16) action=listen-http;; 17) action=listen-grpc;;
   *) printf '请输入菜单中的编号。\n'; continue;;
  esac
  # Ctrl+C leaves the current operation (e.g. live logs), keeping the menu usable.
  if (trap - INT; run_action "$action"); then
   [[ "$action" != uninstall ]] || return 0
  else
   printf '操作已结束，可查看日志了解服务情况。\n'
  fi
 done
}
case "${1:-}" in -h|--help|help) usage; exit 0;; esac
[[ $(id -u) -eq 0 ]] || { fail '请使用 sudo nekopassctl，或由 root 运行。'; exit 1; }
command -v systemctl >/dev/null && [[ -d /run/systemd/system ]] || { fail '需要运行 systemd 的 Linux 服务器。'; exit 1; }
if (($# > 2)); then
 case "$2" in uninstall|listen-http|listen-grpc) ;; *) usage; exit 1;; esac
fi
trap ':' INT
if (($#)); then
 select_service "$@" || exit 1
 shift
 if (($#)); then trap - INT; run_action "$@"; exit $?; fi
 service_menu; exit 0
fi
# The parent ignores Ctrl+C while a menu operation runs in its own subshell.
while true; do
 printf '\nNekopass 服务管理\n1 面板端  2 节点端  3 独立节点服务  0 退出\n请选择：'
 read -r choice || exit 0
 case "$choice" in
  0) exit 0;; 1) target=panel;; 2) target=agent;;
  3) systemctl list-unit-files 'nekopass-agent-*' --no-pager; printf '服务名（不含 .service）：'; read -r target || exit 0;;
  *) printf '请输入菜单中的编号。\n'; continue;;
 esac
 select_service "$target" && service_menu
done
