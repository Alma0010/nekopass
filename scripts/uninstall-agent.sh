#!/usr/bin/env bash
# Standalone uninstaller for the native Linux/systemd Agent installer.
set -Eeuo pipefail
command -v python3 >/dev/null || { printf '需要 Python 3，请先安装 python3。\n' >&2; exit 1; }
exec python3 - "$@" <<'NEKOPASS_UNINSTALL_PYTHON'
import argparse
import contextlib
import os
import pathlib
import re
import shutil
import subprocess
import sys

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
        # A named instance ending in "-update" is recognized by its own config.
        if name.endswith('-update') and not path('/etc/' + name + '/agent.env').exists():
            name = name[:-7]
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
        if name.endswith('-update') and not path('/etc/' + name + '/agent.env').exists():
            name = name[:-7]
        if SERVICE.fullmatch(name):
            found.add(name)
    if path('/etc/nekopass/agent.env').exists() or path('/opt/nekopass/bin/nekopass-agent').exists():
        found.add('nekopass-agent')
    return sorted(found)


def panel_present():
    return (path('/etc/nekopass/control.env').exists()
            or path('/opt/nekopass/bin/nekopass').exists()
            or any(path('/etc/systemd/system').glob('nekopass.service*'))
            or any(path('/etc').glob('nekopass-panel-*'))
            or any(path('/opt').glob('nekopass-panel-*')))


def unit_names(service):
    return [service + '-update.path', service + '-update.service', service + '.service']


def removal_paths(service):
    items = []
    for unit in unit_names(service):
        items.extend([path('/etc/systemd/system/' + unit), path('/etc/systemd/system/' + unit + '.d')])
        for pattern in ['*.wants', '*.requires']:
            items.extend(folder / unit for folder in path('/etc/systemd/system').glob(pattern) if (folder / unit).is_symlink())
    items.append(path('/var/lib/' + service))
    if service == 'nekopass-agent':
        items.append(path('/etc/nekopass/agent.env'))
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


def main(argv=None):
    parser = argparse.ArgumentParser(prog='uninstall-agent.sh', description='卸载本机 Nekopass 节点。默认完整卸载所有节点及其配置、状态、更新服务和专用账户；不删除面板或数据库。')
    parser.add_argument('--service-name', '-s', help='仅卸载指定节点，例如 nekopass-agent-hk1（可带 .service）')
    parser.add_argument('--dry-run', action='store_true', help='只列出卸载范围，不修改系统')
    args = parser.parse_args(argv)
    requested = valid_service(args.service_name) if args.service_name else None
    if not sys.platform.startswith('linux'):
        raise ValueError('需要 Linux / systemd')
    if os.geteuid() != 0:
        raise ValueError('请以 root 或 sudo 运行')
    if not path('/run/systemd/system').is_dir() or shutil.which('systemctl') is None:
        raise ValueError('需要运行 systemd 的 Linux 主机')
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
        base = '/opt/nekopass' if service == 'nekopass-agent' else '/opt/' + service
        for unit in unit_names(service):
            state = command('systemctl', 'show', unit, '-p', 'LoadState', '--value', check=False)
            if state.returncode and state.stdout.strip() != 'not-found':
                raise RuntimeError('无法读取服务状态，卸载未开始：' + unit)
            states[unit] = state.stdout.strip()
            if states[unit] == 'loaded' and unit.endswith('.service'):
                executable = command('systemctl', 'show', unit, '-p', 'ExecStart', '--value').stdout
                expected = base + '/bin/' + ('nekopass-agent' if unit == service + '.service' else 'nekopass-update')
                if executable.strip() and not re.search(r'(?:path=|^)' + re.escape(expected) + r'(?:[ ;]|$)', executable.strip()):
                    raise ValueError('服务执行文件不属于此节点，拒绝卸载：' + unit)
    if not services:
        log('没有发现已安装的节点；检查孤立的共享管理脚本。')
    for service, items in plans.items():
        log(('预览卸载：' if args.dry_run else '卸载节点：') + service)
        for item in items:
            if item.exists() or item.is_symlink():
                log('删除 ' + str(item))
    for service in users:
        log('删除专用账户 ' + service)
    for item in shared:
        if item.exists():
            log('删除 ' + str(item))
    if args.dry_run:
        log('预览完成，未修改系统。')
        return 0
    # Stop all update watchers before acquiring their locks. Otherwise an updater
    # could replace files or restart a node while it is being uninstalled.
    for service in services:
        for unit in unit_names(service):
            if states[unit] not in ['', 'not-found']:
                command('systemctl', 'stop', unit)
                if states[unit] not in ['masked', 'masked-runtime'] and unit != service + '-update.service':
                    command('systemctl', 'disable', unit)
    import fcntl
    with contextlib.ExitStack() as locks:
        for service in services:
            lock = path('/run/lock/' + service + '-update.lock')
            check_path(lock)
            if lock.is_symlink():
                raise ValueError('更新锁不能是符号链接：' + str(lock))
            handle = locks.enter_context(lock.open('a'))
            try:
                fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                raise RuntimeError('节点仍在更新，请等待更新完成后重试：' + service)
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
    log('卸载完成。面板中的节点记录不会自动删除，可在后台按需清理。')
    return 0


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (OSError, ValueError, RuntimeError) as error:
        print('[nekopass] 卸载失败：' + str(error), file=sys.stderr)
        sys.exit(1)
NEKOPASS_UNINSTALL_PYTHON
