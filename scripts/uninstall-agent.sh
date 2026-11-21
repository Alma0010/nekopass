#!/usr/bin/env bash
# Standalone uninstaller for the native Linux/systemd Agent installer.
set -Eeuo pipefail
command -v python3 >/dev/null || { printf '需要 Python 3，请先安装 python3。\n' >&2; exit 1; }
exec python3 - "$@" <<'NEKOPASS_UNINSTALL_PYTHON'
import argparse
import contextlib
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

ROOT = pathlib.Path('/')
SERVICE = re.compile(r'nekopass-agent(?:-[a-z0-9]{1,16})?')


def path(name):
    return ROOT / name.lstrip('/')


def log(message):
    print('[nekopass] ' + message, flush=True)


def command(*args, check=True):
    result = subprocess.run(list(args), text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if check and result.returncode:
        # Never print unit/environment contents or credentials from command output.
        raise RuntimeError('命令失败：' + ' '.join(args))
    return result


def valid_service(value):
    value = value.removesuffix('.service')
    if not SERVICE.fullmatch(value):
        raise ValueError('服务名须为 nekopass-agent 或 nekopass-agent-加上 1–16 位小写字母/数字')
    return value


def discover():
    found = set()
    import pwd
    found.update(user.pw_name for user in pwd.getpwall() if SERVICE.fullmatch(user.pw_name))
    # Inventory files as well as units, so interrupted installations can be removed.
    for parent in ['/opt', '/etc', '/var/lib']:
        for item in path(parent).glob('nekopass-agent*'):
            if SERVICE.fullmatch(item.name):
                found.add(item.name)
    for item in path('/etc/systemd/system').glob('nekopass-agent*'):
        name = item.name
        if name.endswith(('.service.d', '.path.d')):
            name = name[:-len('.service.d')] if name.endswith('.service.d') else name[:-len('.path.d')]
        elif name.endswith(('.service', '.path')):
            name = name.rsplit('.', 1)[0]
        else:
            continue
        name = managed_service_name(name)
        if SERVICE.fullmatch(name):
            found.add(name)
    result = command('systemctl', 'list-unit-files', '--no-legend', '--no-pager', 'nekopass-agent*', check=False)
    if result.returncode:
        raise RuntimeError('无法列出节点服务，卸载未开始')
    for line in result.stdout.splitlines():
        fields = line.split()
        if not fields or not fields[0].endswith(('.service', '.path')):
            continue
        name = fields[0].rsplit('.', 1)[0]
        name = managed_service_name(name)
        if SERVICE.fullmatch(name):
            found.add(name)
    if path('/etc/nekopass/agent.env').exists() or path('/opt/nekopass/bin/nekopass-agent').exists():
        found.add('nekopass-agent')
    return sorted(found)


def managed_service_name(name):
    for suffix in ('-update', '-uninstall'):
        if name.endswith(suffix) and not path('/etc/' + name + '/agent.env').exists():
            return name[:-len(suffix)]
    return name


def panel_present():
    return (path('/etc/nekopass/control.env').exists()
            or path('/opt/nekopass/bin/nekopass').exists()
            or any(path('/etc/systemd/system').glob('nekopass.service*'))
            or any(path('/etc').glob('nekopass-panel-*'))
            or any(path('/opt').glob('nekopass-panel-*')))


def unit_names(service):
    units = [service + '-uninstall.path', service + '-uninstall.service',
             service + '-update.path', service + '-update.service', service + '.service']
    # A legacy named instance may use the default helper's unit name.
    if service == 'nekopass-agent' and path('/etc/nekopass-agent-uninstall/agent.env').exists():
        units.remove('nekopass-agent-uninstall.service')
    return units


def install_base(service):
    return '/opt/nekopass' if service == 'nekopass-agent' else '/opt/' + service


def agent_env(service):
    config = 'nekopass' if service == 'nekopass-agent' else service
    return path('/etc/' + config + '/agent.env')


def removal_paths(service):
    items = []
    for unit in unit_names(service):
        items.extend([path('/etc/systemd/system/' + unit), path('/etc/systemd/system/' + unit + '.d')])
        for pattern in ['*.wants', '*.requires']:
            items.extend(folder / unit for folder in path('/etc/systemd/system').glob(pattern) if (folder / unit).is_symlink())
    items.append(path('/var/lib/' + service))
    if service == 'nekopass-agent':
        items.append(agent_env(service))
        items.append(path('/opt/nekopass/bin/nekopass-agent-uninstall'))
        items.append(path('/opt/nekopass/bin/nekopass-agent-update-source.json'))
        items.extend(path('/etc/nekopass').glob('agent.env.bak.*'))
        for suffix in ['', '.previous', '.next', '.update-pending']:
            items.append(path('/opt/nekopass/bin/nekopass-agent' + suffix))
        # The updater in this directory is shared with the default panel.
        if not panel_present():
            items.append(path('/opt/nekopass/bin/nekopass-update'))
    else:
        if path('/etc/' + service + '/control.env').exists() or path('/opt/' + service + '/bin/nekopass').exists():
            raise ValueError('节点目录内发现面板文件，拒绝删除：' + service)
        items.extend([path('/etc/' + service), path('/opt/' + service)])
    return items


def check_path(target):
    # Check every ancestor before any service is stopped; unlinking a leaf symlink
    # never follows it, but recursive deletion must not cross a directory symlink.
    if target.parent.resolve() != target.parent:
        raise ValueError('拒绝经过符号链接的卸载路径：' + str(target))
    if target.is_symlink() and (target.is_dir() or target.name.endswith('.d')):
        raise ValueError('拒绝递归删除符号链接目录：' + str(target))


def remove(target):
    check_path(target)
    if target.is_symlink():
        target.unlink()
    elif target.is_dir():
        shutil.rmtree(target)
    else:
        target.unlink(missing_ok=True)


def users_for(services):
    import pwd
    result = []
    for service in services:
        try:
            user = pwd.getpwnam(service)
        except KeyError:
            continue
        # Installer-created accounts have no login/home and are system accounts.
        if user.pw_uid == 0 or user.pw_uid >= 1000 or user.pw_dir != '/nonexistent' or user.pw_shell not in ['/usr/sbin/nologin', '/sbin/nologin', '/bin/false', '/usr/bin/false']:
            raise ValueError('同名账户不是安装脚本创建的系统账户，拒绝卸载：' + service)
        result.append(service)
    return result


def remove_user(service):
    import grp
    import pwd
    command('userdel', service)  # Never use --remove on an account's home directory.
    try:
        group = grp.getgrnam(service)
    except KeyError:
        return
    if group.gr_mem or any(user.pw_gid == group.gr_gid for user in pwd.getpwall()):
        log('保留仍被其他账户使用的用户组：' + service)
        return
    command('groupdel', service)


def update_lock(service):
    target = path('/run/lock/' + service + '-update.lock')
    check_path(target)
    if target.is_symlink():
        raise ValueError('更新锁不能是符号链接：' + str(target))
    flags = os.O_RDWR | os.O_CREAT | getattr(os, 'O_NOFOLLOW', 0) | getattr(os, 'O_NONBLOCK', 0)
    file = os.fdopen(os.open(target, flags, 0o600), 'a')
    if os.name == 'posix':
        info = os.fstat(file.fileno())
        if (not stat.S_ISREG(info.st_mode) or info.st_nlink != 1
                or info.st_uid != ROOT.stat().st_uid or info.st_mode & 0o022):
            file.close()
            raise ValueError('更新锁的归属或权限不正确')
    return file


def uninstall(requested, *, remote=False):
    services = [requested] if requested else discover()
    users = users_for(services)
    plans = {service: removal_paths(service) for service in services}
    shared = []
    if not requested and not panel_present():
        shared.append(path('/usr/local/bin/nekopassctl'))
    for target in [item for items in plans.values() for item in items] + shared:
        check_path(target)
    states = {}
    for service in services:
        base = install_base(service)
        executables = {
            service + '.service': 'nekopass-agent',
            service + '-uninstall.service': 'nekopass-agent-uninstall',
            service + '-update.service': 'nekopass-update',
        }
        for unit in unit_names(service):
            state = command('systemctl', 'show', unit, '-p', 'LoadState', '--value', check=False)
            if state.returncode and state.stdout.strip() != 'not-found':
                raise RuntimeError('无法读取服务状态，卸载未开始：' + unit)
            states[unit] = state.stdout.strip()
            if states[unit] == 'loaded' and unit.endswith('.service'):
                executable = command('systemctl', 'show', unit, '-p', 'ExecStart', '--value').stdout
                expected = base + '/bin/' + executables[unit]
                if executable.strip() and not re.search(r'(?:path=|^)' + re.escape(expected) + r'(?:[ ;]|$)', executable.strip()):
                    raise ValueError('服务执行文件不属于此节点，拒绝卸载：' + unit)
        if remote and states[service + '.service'] != 'loaded':
            raise ValueError('找不到此节点的服务，卸载未开始')
    if not services:
        log('没有发现已安装的节点；检查孤立的共享管理脚本。')
    for service, items in plans.items():
        log('卸载节点：' + service)
        for item in items:
            if item.exists() or item.is_symlink():
                log('删除 ' + str(item))
    for service in users:
        log('删除专用账户 ' + service)
    for item in shared:
        if item.exists():
            log('删除 ' + str(item))
    import fcntl
    with contextlib.ExitStack() as locks:
        for service in services:
            handle = locks.enter_context(update_lock(service))
            try:
                fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                raise RuntimeError('节点仍在更新，请等待更新完成后重试：' + service)
        # Acquire the updater lock before stopping the Agent so a busy updater
        # leaves its running service alone. The request helper must stay alive
        # through removal so it can report back to the panel.
        for service in services:
            for unit in unit_names(service):
                if remote and unit == service + '-uninstall.service':
                    continue
                if states[unit] not in ['', 'not-found']:
                    command('systemctl', 'stop', unit)
                    if states[unit] not in ['masked', 'masked-runtime'] and unit in (service + '.service', service + '-uninstall.path', service + '-update.path'):
                        command('systemctl', 'disable', unit)
        for items in plans.values():
            for item in items:
                remove(item)
        for service in users:
            remove_user(service)
        for item in shared:
            remove(item)
        command('systemctl', 'daemon-reload')
        for service in services:
            command('systemctl', 'reset-failed', *unit_names(service), check=False)
    for service in services:
        remove(path('/run/lock/' + service + '-update.lock'))
    # Only remove empty shared directories, preserving every panel/unknown file.
    for directory in (['/etc/nekopass', '/opt/nekopass/bin', '/opt/nekopass'] if not requested or requested == 'nekopass-agent' else []):
        target = path(directory)
        check_path(target)
        if target.is_dir():
            try:
                target.rmdir()
            except OSError:
                pass
    if remote:
        log('本机卸载完成，正在向面板回报结果。')
    else:
        log('卸载完成。面板中的节点记录可在后台按需清理。')
    return 0


def open_regular(target, owner, *, protected=False):
    check_path(target)
    if protected:
        for parent in target.parents:
            if parent == ROOT:
                break
            info = parent.stat()
            if info.st_uid != 0 or info.st_mode & 0o022:
                raise ValueError('拒绝使用可被其他账户修改的安装目录')
    fd = os.open(target, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    file = os.fdopen(fd, 'rb')
    info = os.fstat(fd)
    if (not stat.S_ISREG(info.st_mode) or info.st_uid != owner or info.st_nlink != 1
            or (protected and info.st_mode & 0o022)):
        file.close()
        raise ValueError('卸载请求或安装文件的类型与权限不正确')
    return file


def consume_request(service):
    import pwd
    request = path('/var/lib/' + service + '/uninstall-request.json')
    check_path(request)
    if not stat.S_ISREG(request.lstat().st_mode):
        if not request.is_dir() or request.is_symlink():
            request.unlink(missing_ok=True)
        raise ValueError('无效的节点卸载请求文件')
    # Claim the message into a root-owned directory before reading it. The
    # normal request writer can then redeliver if authorization is unavailable.
    with tempfile.TemporaryDirectory(prefix='.' + service + '-uninstall-', dir=request.parent.parent) as directory:
        claimed = pathlib.Path(directory) / 'request.json'
        request.rename(claimed)
        with open_regular(claimed, pwd.getpwnam(service).pw_uid) as file:
            data = file.read(513)
    value = json.loads(data) if len(data) <= 512 else None
    ticket = value.get('ticket') if isinstance(value, dict) else None
    if not isinstance(ticket, str) or not re.fullmatch(r'[0-9a-f]{64}', ticket):
        raise ValueError('无效的节点卸载请求')
    return ticket


def report_environment(service):
    with open_regular(agent_env(service), 0, protected=True) as file:
        if os.fstat(file.fileno()).st_mode & 0o077:
            raise ValueError('节点配置应仅供 root 读取')
        data = file.read(16385)
    if len(data) > 16384:
        raise ValueError('节点配置超出大小限制')
    values = {}
    keys = {'NEKOPASS_SERVER', 'NEKOPASS_NODE_TOKEN'}
    for line in data.decode('utf-8').splitlines():
        key, _, value = line.partition('=')
        if key.strip() in keys:
            value = value.strip()
            if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
                value = value[1:-1]
            values[key.strip()] = value
    if (not re.fullmatch(r'\S+', values.get('NEKOPASS_SERVER', ''))
            or not re.fullmatch(r'[A-Za-z0-9_-]{8,128}', values.get('NEKOPASS_NODE_TOKEN', ''))):
        raise ValueError('节点连接配置不完整')
    return dict(os.environ, **values)


def process_request(service):
    try:
        ticket = consume_request(service)
    except (OSError, ValueError, KeyError):
        time.sleep(2)
        raise ValueError('无法读取节点卸载请求') from None
    env = report_environment(service)
    with open_regular(path(install_base(service) + '/bin/nekopass-agent'), 0, protected=True) as reporter:
        fd = reporter.fileno()
        if not os.fstat(fd).st_mode & stat.S_IXUSR:
            raise ValueError('节点程序不可执行')
        args = [f'/proc/self/fd/{fd}', '-uninstall-result', ticket]
        if not send_result(args + ['-uninstall-check'], env, fd):
            time.sleep(2)
            raise RuntimeError('无法向面板确认卸载授权，节点仍保留')
        try:
            uninstall(service, remote=True)
        except (OSError, ValueError, RuntimeError) as error:
            print('[nekopass] 卸载失败：' + str(error), file=sys.stderr)
            args.extend(['-uninstall-error', '本机卸载失败，请查看节点卸载服务日志。'])
        # The validated executable stays open even after its installed path is removed.
        if not send_result(args, env, fd):
            raise RuntimeError('无法向面板报告卸载结果')
        return 1 if len(args) > 3 else 0


def send_result(args, env, fd):
    try:
        result = subprocess.run(args, env=env, pass_fds=(fd,), capture_output=True, timeout=60)
    except (OSError, subprocess.SubprocessError):
        return False
    return result.returncode == 0


def main(argv=None):
    parser = argparse.ArgumentParser(prog='uninstall-agent.sh', description='卸载本机 Nekopass 节点。默认完整卸载所有节点及其配置、状态、更新服务和专用账户；不删除面板或数据库。')
    parser.add_argument('--service-name', '-s', help='仅卸载指定节点，例如 nekopass-agent-hk1（可带 .service）')
    parser.add_argument('--request', action='store_true', help=argparse.SUPPRESS)
    args = parser.parse_args(argv)
    requested = valid_service(args.service_name) if args.service_name else None
    if args.request and not requested:
        parser.error('--request requires --service-name')
    if not sys.platform.startswith('linux'):
        raise ValueError('需要 Linux / systemd')
    if os.geteuid() != 0:
        raise ValueError('请以 root 或 sudo 运行')
    if not path('/run/systemd/system').is_dir() or shutil.which('systemctl') is None:
        raise ValueError('需要运行 systemd 的 Linux 主机')
    return process_request(requested) if args.request else uninstall(requested)


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (OSError, ValueError, RuntimeError) as error:
        print('[nekopass] 卸载失败：' + str(error), file=sys.stderr)
        sys.exit(1)
NEKOPASS_UNINSTALL_PYTHON
