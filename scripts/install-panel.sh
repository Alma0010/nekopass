#!/usr/bin/env bash
# Native Debian/Ubuntu installation. Web stays HTTP; TLS is for Agent control only.
set -Eeuo pipefail
umask 077
write_manager_payload() { return 1; }
# PACKAGED_MANAGER
write_updater_payload() { return 1; }
# PACKAGED_UPDATER
DEFAULT_DOWNLOAD_BASE='https://github.com/Alma0010/nekopass/releases/download'
RELEASE_VERSION='v0.20.6'; DOWNLOAD_BASE=$DEFAULT_DOWNLOAD_BASE; PACKAGE_URL=''; PACKAGE=''; SOURCE_DIR=''
SERVICE='nekopass'; HOST=''; AGENT_HOST=''; AGENT_PORT=''; HTTP_PORT=8080; GRPC_PORT=9443; DB_PORT=''; PG_VERSION=''
PG_SOURCE=system; ADMIN=admin; SITE=Nekopass; TLS_MODE=plain; TLS_CERT=''; TLS_KEY=''
DATABASE_URL_FILE=''; external_database=0
AGENT_INSTALLER='https://github.com/Alma0010/nekopass/releases/latest/download/install-agent.sh'; AGENT_RELEASES='https://github.com/Alma0010/nekopass/releases/download'; FIREWALL=auto; YES=0; SKIP_DEPS=0; DRY_RUN=0; REINSTALL=0; WORK=''
die() { printf '[nekopass] %s\n' "$*" >&2; exit 1; }
install_shared_tool() {
 python3 - "$1" "$2" <<'NEKOPASS_INSTALL_TOOL'
import fcntl
import os
import subprocess
import sys

source, target = sys.argv[1:]
directory = os.open(os.path.dirname(target), os.O_RDONLY | os.O_DIRECTORY)
try:
    fcntl.flock(directory, fcntl.LOCK_EX)
    subprocess.run(['install', '-m', '755', source, target], check=True)
finally:
    os.close(directory)
NEKOPASS_INSTALL_TOOL
}
log() { printf '[nekopass] %s\n' "$*"; }
postgres_run() { (cd / && runuser -u postgres -- "$@"); }
usage() {
 cat <<'HELP'
Nekopass 面板一键安装（Debian/Ubuntu，原生 systemd，以 root 执行）

  bash install-panel.sh                           交互式安装
  bash install-panel.sh --package /path/panel.tar.gz
  bash install-panel.sh --download-base https://github.com/Alma0010/nekopass/releases/download

  --package PATH           本地面板发布包
  --package-url URL        直接下载 HTTPS 发布包
  --download-base URL      HTTPS 版本下载根地址
  --source-dir PATH        已编译的源码目录（dist/bin + web/dist）
  --version VERSION        默认 v0.20.6
  --host HOST              用户/节点能访问的域名或 IP，不含协议
  --http-port PORT         网页 HTTP 端口，默认 8080
  --grpc-port PORT         节点连接端口，默认 9443
  --agent-host HOST        节点连接域名/IP，默认与面板相同
  --agent-port PORT        节点对外端口，默认与节点监听相同；代理模式默认 443
  --postgres-source NAME   新装 PostgreSQL 时使用 system（系统源）或 pgdg（官方源）
  --postgres-version N     选择可用 PostgreSQL 主版本
  --db-port PORT           指定本机 PostgreSQL 集群端口
  --database-url-file PATH  连接已建好的本机 PostgreSQL，读取仅 root 可读的单行连接地址
  --admin USER             初始管理员，默认 admin；密码随机生成
  --site-name NAME         站点名称
  --agent-tls MODE         plain（测试明文 HTTP/2）、existing（公共 CA 证书）、proxy（已有 TLS 反向代理）
  --tls-cert PATH          existing 模式的证书链
  --tls-key PATH           existing 模式的私钥
  --agent-installer URL    可选节点安装脚本 HTTPS 地址
  --agent-releases URL     可选节点版本下载根地址
  --firewall MODE          auto 自动放行活动的 UFW/firewalld；skip 跳过
  --service-name NAME      默认 nekopass，测试可用 nekopass-panel-xxx
  --reinstall              清空该服务的面板数据库并全新安装，忽略已有 control.env
  --yes                    使用参数和默认值，不询问
  --skip-dependencies      禁止通过 APT 安装软件包，缺少必需依赖时停止
  --dry-run                只验证参数，不下载或修改机器
  -h, --help               显示帮助

安装完成：nekopassctl；忘记密码：nekopassctl panel reset-password
control.env 存在时不会重复安装；配置文件不存在时可重新安装，保留同名数据库与已有管理员。
--reinstall 会删除所选数据库中的用户、余额、订单、节点和规则；--reinstall --yes 不询问确认。
HELP
}
value() { [[ $# -ge 2 && -n "$2" ]] || die "参数 $1 缺少值"; }
while (($#)); do
 case "$1" in
  -h|--help) usage; exit 0;;
  --package) value "$@"; PACKAGE=$2; shift 2;;
  --package-url) value "$@"; PACKAGE_URL=$2; shift 2;;
  --download-base) value "$@"; DOWNLOAD_BASE=${2%/}; shift 2;;
  --source-dir) value "$@"; SOURCE_DIR=$2; shift 2;;
  --version) value "$@"; RELEASE_VERSION=$2; shift 2;;
  --host) value "$@"; HOST=$2; shift 2;;
  --http-port) value "$@"; HTTP_PORT=$2; shift 2;;
  --grpc-port) value "$@"; GRPC_PORT=$2; shift 2;;
  --agent-host) value "$@"; AGENT_HOST=$2; shift 2;;
  --agent-port) value "$@"; AGENT_PORT=$2; shift 2;;
  --postgres-source) value "$@"; PG_SOURCE=$2; shift 2;;
  --postgres-version) value "$@"; PG_VERSION=$2; shift 2;;
  --db-port) value "$@"; DB_PORT=$2; shift 2;;
  --database-url-file) value "$@"; DATABASE_URL_FILE=$2; shift 2;;
  --admin) value "$@"; ADMIN=$2; shift 2;;
  --site-name) value "$@"; SITE=$2; shift 2;;
  --agent-tls) value "$@"; TLS_MODE=$2; shift 2;;
  --tls-cert) value "$@"; TLS_CERT=$2; shift 2;;
  --tls-key) value "$@"; TLS_KEY=$2; shift 2;;
  --agent-installer) value "$@"; AGENT_INSTALLER=$2; shift 2;;
  --agent-releases) value "$@"; AGENT_RELEASES=$2; shift 2;;
  --firewall) value "$@"; FIREWALL=$2; shift 2;;
  --service-name) value "$@"; SERVICE=$2; shift 2;;
  --yes) YES=1; shift;;
  --reinstall) REINSTALL=1; shift;;
  --skip-dependencies) SKIP_DEPS=1; shift;;
  --dry-run) DRY_RUN=1; shift;;
  *) die "未知参数：$1";;
 esac
done
[[ "$SERVICE" == nekopass || "$SERVICE" =~ ^nekopass-panel-[a-z0-9]{1,16}$ ]] || die '服务名不正确'
[[ "$RELEASE_VERSION" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ && "$RELEASE_VERSION" != *..* ]] || die '版本格式不正确'
case "$(uname -m)" in x86_64|amd64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; *) die '只支持 amd64 / arm64';; esac
[[ "$(uname -s)" == Linux ]] || die '需要 Linux'
port_ok() { [[ "$1" =~ ^[0-9]{1,5}$ ]] && ((10#$1 >= 1 && 10#$1 <= 65535)); }
port_ok "$HTTP_PORT" && port_ok "$GRPC_PORT" || die '端口须为 1–65535'
[[ "$HTTP_PORT" != "$GRPC_PORT" ]] || die '网页和节点连接端口不能相同'
[[ -z "$DB_PORT" ]] || port_ok "$DB_PORT" || die '数据库端口不正确'
[[ -z "$DB_PORT" ]] || DB_PORT=$((10#$DB_PORT))
[[ -z "$AGENT_PORT" ]] || port_ok "$AGENT_PORT" || die '节点对外端口不正确'
if [[ -n "$PG_VERSION" ]]; then
 [[ "$PG_VERSION" =~ ^[0-9]{2}$ ]] && ((10#$PG_VERSION >= 14)) || die '数据库主版本须为 PostgreSQL 14 或以上'
fi
if [[ -n "$DATABASE_URL_FILE" ]]; then
 [[ -z "$PG_VERSION" && -z "$DB_PORT" ]] || die '连接地址文件已指定数据库，请省略 --postgres-version 和 --db-port'
 ((!REINSTALL)) || die '已有数据库模式不支持 --reinstall'
fi
[[ "$PG_SOURCE" == system || "$PG_SOURCE" == pgdg ]] || die '数据库来源不正确'
[[ "$TLS_MODE" == plain || "$TLS_MODE" == existing || "$TLS_MODE" == proxy ]] || die '节点 TLS 模式不正确'
[[ "$FIREWALL" == auto || "$FIREWALL" == skip ]] || die '防火墙模式不正确'
if ((DRY_RUN)); then
 database_plan="$PG_SOURCE/${PG_VERSION:-默认}"
 [[ -z "$DATABASE_URL_FILE" ]] || database_plan='已有 PostgreSQL（连接地址文件）'
 log "计划：linux/$ARCH，服务 $SERVICE，网页 $HTTP_PORT，节点 $GRPC_PORT，数据库 $database_plan，版本 $RELEASE_VERSION，清空重装 $REINSTALL"
 exit 0
fi
[[ $(id -u) -eq 0 ]] || die '请以 root 执行'
[[ -d /run/systemd/system ]] && command -v systemctl >/dev/null || die '需要正在运行的 systemd'
. /etc/os-release
[[ "$ID" == debian || "$ID" == ubuntu ]] || die '当前安装器支持 Debian / Ubuntu'
BASE="/opt/$SERVICE"; CONFIG="/etc/$SERVICE"; [[ "$SERVICE" != nekopass ]] || CONFIG=/etc/nekopass
UNIT="/etc/systemd/system/$SERVICE.service"
((REINSTALL)) || [[ ! -e "$CONFIG/control.env" && ! -L "$CONFIG/control.env" ]] || die '此服务已有配置，请使用 nekopassctl 管理或按升级方式更新，安装器不会覆盖它'
STATE="/var/lib/$SERVICE"
for target in "$BASE" "$BASE/web" "$CONFIG" "$CONFIG/control.env" "$CONFIG/tls" "$STATE" "$UNIT" /usr/local/bin/nekopassctl; do
 [[ ! -L "$target" && "$(realpath -m "$target")" == "$target" ]] || die '安装目标不能是符号链接';
done
if ((REINSTALL)) && [[ -f "$CONFIG/control.env" ]]; then
 database_count=$(grep -Ec '^[[:space:]]*NEKOPASS_DATABASE_URL[[:space:]]*=' "$CONFIG/control.env" || true)
 [[ "$database_count" == 1 ]] &&
  grep -Eq "^NEKOPASS_DATABASE_URL=postgres://$SERVICE@/$SERVICE[?]host=/var/run/postgresql&port=[0-9]{1,5}&sslmode=disable$" "$CONFIG/control.env" ||
  die '无法确认当前配置使用系统 PostgreSQL，已停止清空重装'
fi
prompt() {
 local key=$1 label=$2 default=$3 answer
 if ((YES)); then printf -v "$key" '%s' "$default"; return; fi
 # Readline handles cursor keys, Home/End, Delete and Backspace. Keep the
 # default in the editable buffer, including when the script uses piped stdin.
 if [[ -t 0 ]]; then
  read -r -e -i "$default" -p "$label [$default]：" answer || die '交互输入已结束'
 else
  read -r -e -i "$default" -p "$label [$default]：" answer </dev/tty || die '无法交互，请使用 --yes 并提供参数'
 fi
 printf -v "$key" '%s' "${answer:-$default}"
}
postgres_installed() { [[ -x "/usr/lib/postgresql/$1/bin/postgres" ]]; }
installed_postgres_versions() {
 local path version
 for path in /usr/lib/postgresql/*; do
  version=${path##*/}
  [[ "$version" =~ ^[0-9]{2}$ ]] && ((10#$version >= 14)) && postgres_installed "$version" || continue
  printf '%s\n' "$version"
 done | sort -n
}
postgres_port_in_use() {
 local port=$1 port_hex table
 [[ -S "/var/run/postgresql/.s.PGSQL.$port" || -S "/tmp/.s.PGSQL.$port" ]] && return 0
 printf -v port_hex '%04X' "$port"
 for table in /proc/net/tcp /proc/net/tcp6; do
  [[ -r "$table" ]] || continue
  if awk -v port="$port_hex" '$4 == "0A" && $2 ~ (":" port "$") { found=1; exit } END { exit !found }' "$table"; then
   return 0
  fi
 done
 return 1
}
select_postgres_cluster() {
 local version name port status choice default=1 online=0 i
 local -a clusters=()
 while read -r version name port status _; do
  [[ "$version" =~ ^[0-9]{2}$ ]] && ((10#$version >= 14)) && postgres_installed "$version" || continue
  [[ "$status" == online || "$status" == down ]] || continue
  [[ -z "$PG_VERSION" || "$PG_VERSION" == "$version" ]] || continue
  [[ -z "$DB_PORT" || "$DB_PORT" == "$port" ]] || continue
  clusters+=("$version $name $port $status")
  if [[ "$status" == online ]] && ((!online)); then
   default=${#clusters[@]}; online=1
  fi
 done <<< "$1"
 ((${#clusters[@]} > 0)) || return 1
 choice=$default
 if ((!YES)) && [[ -z "$DB_PORT" ]]; then
  log '检测到本机 PostgreSQL 集群：'
  for i in "${!clusters[@]}"; do
   read -r version name port status <<< "${clusters[i]}"
   [[ "$status" != online ]] || status='运行中'
   [[ "$status" != down ]] || status='已停止'
   printf '  %s) 沿用 PostgreSQL %s · %s · %s（%s）\n' "$((i+1))" "$version" "$name" "$port" "$status"
  done
  local new_choice=$((${#clusters[@]}+1)) external_choice=$((${#clusters[@]}+2))
  printf '  %s) 安装或创建新的 PostgreSQL 集群\n' "$new_choice"
  printf '  %s) 连接已建好的 PostgreSQL（例如 Docker）\n' "$external_choice"
  while true; do
   prompt choice '选择 PostgreSQL 集群' "$default"
   if [[ "$choice" =~ ^[1-9][0-9]{0,8}$ ]] && ((choice <= external_choice)); then break; fi
   log '请输入菜单中的编号。'
  done
  if ((choice == new_choice)); then new_pg_cluster=1; return 1; fi
  if ((choice == external_choice)); then external_database=1; return 1; fi
 fi
 read -r PG_VERSION selected_pg_cluster DB_PORT _ <<< "${clusters[choice-1]}"
 log "沿用 PostgreSQL $PG_VERSION · $selected_pg_cluster · $DB_PORT"
}
select_database() {
 initial_pg_clusters=''
 selected_pg_cluster=''
 new_pg_cluster=0
 if [[ -n "$DATABASE_URL_FILE" ]]; then
  external_database=1
 elif command -v pg_lsclusters >/dev/null; then
  initial_pg_clusters=$(pg_lsclusters --no-header)
  select_postgres_cluster "$initial_pg_clusters" || true
 fi
 if ((!external_database && !new_pg_cluster)) && [[ -z "$selected_pg_cluster" ]]; then
  local port=${DB_PORT:-5432}
  if ! awk -v p="$port" '$3==p {found=1} END {exit !found}' <<< "$initial_pg_clusters" && postgres_port_in_use "$port"; then
   ((!YES)) || die "端口 $port 已有服务，请用 --database-url-file 指定已有 PostgreSQL"
   log "端口 $port 已被系统集群列表之外的服务占用，可连接已建好的 PostgreSQL（例如 Docker）。"
   external_database=1
  fi
 fi
 if ((external_database)); then
  ((!REINSTALL)) || die '已有数据库模式不支持 --reinstall'
  DB_PORT=${DB_PORT:-5432}
 fi
}
ensure_apt_indexes() {
 if ((!apt_indexes_ready)); then
  apt-get update
  apt_indexes_ready=1
 fi
}
prepare_install_dependencies() {
 local ca_bundle=${1:-/etc/ssl/certs/ca-certificates.crt} cmd
 local -a required=(curl python3 openssl systemd-run)
 local -a missing=()
 if ((!SKIP_DEPS)); then
  command -v curl >/dev/null || missing+=(curl)
  [[ -s "$ca_bundle" ]] || missing+=(ca-certificates)
  command -v python3 >/dev/null || missing+=(python3)
  command -v openssl >/dev/null || missing+=(openssl)
  if ((external_database)); then
   psql --version >/dev/null 2>&1 || missing+=(postgresql-client)
  else
   command -v pg_lsclusters >/dev/null && command -v pg_createcluster >/dev/null && command -v pg_ctlcluster >/dev/null ||
    missing+=(postgresql-common)
  fi
  if ((${#missing[@]})); then
   log "安装缺少的依赖：${missing[*]}"
   ensure_apt_indexes
   apt-get install -y "${missing[@]}"
  fi
 fi
 if ((external_database)); then
  psql --version >/dev/null 2>&1 || die '缺少 PostgreSQL 客户端（psql）'
 else
  required+=(pg_lsclusters pg_createcluster pg_ctlcluster)
 fi
 for cmd in "${required[@]}"; do
  command -v "$cmd" >/dev/null || die "缺少依赖：$cmd"
 done
}
external_postgres() {
 python3 - "$WORK/control.env" "$@" <<'PY'
import os
import pathlib
import re
import stat
import subprocess
import sys
import tempfile
import urllib.parse as url

config = pathlib.Path(sys.argv[1])
action, *args = sys.argv[2:]
invalid = '已有数据库需要本机 PostgreSQL 连接地址、数据库名、用户和密码'
try:
    if action == 'configure' and not args[0]:
        port, database, user = args[1:]
        host, sslmode = '127.0.0.1', 'disable'
        password = os.environ['NEKOPASS_INSTALL_DB_PASSWORD']
    else:
        if action == 'configure':
            path = pathlib.Path(args[0])
            info = path.lstat()
            if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o077 or info.st_size > 8192:
                sys.exit('连接地址文件必须是仅 root 可读的普通文件')
            raw = path.read_text(encoding='utf-8').rstrip('\n')
        else:
            raw = config.read_text(encoding='utf-8').splitlines()[0].split('=', 1)[1]
        if any(ord(char) < 33 for char in raw) or re.search(r'%(?![0-9A-Fa-f]{2})', raw):
            raise ValueError()
        parsed = url.urlsplit(raw)
        if (parsed.scheme not in ('postgres', 'postgresql') or parsed.fragment
                or parsed.query not in ('', 'sslmode=disable', 'sslmode=require')
                or not parsed.path.startswith('/') or '/' in parsed.path[1:]):
            raise ValueError()
        host = '127.0.0.1' if parsed.hostname == 'localhost' else parsed.hostname
        port = str(parsed.port if parsed.port is not None else 5432)
        user, password, database = (
            url.unquote(value or '', errors='strict')
            for value in (parsed.username, parsed.password, parsed.path[1:])
        )
        sslmode = 'require' if parsed.query == 'sslmode=require' else 'disable'
    # pgx strips leading slashes from the decoded database path.
    if database.startswith('/'):
        sys.exit('数据库名不能以 / 开头')
    if (host not in ('127.0.0.1', '::1') or not re.fullmatch(r'[0-9]{1,5}', port)
            or not 1 <= int(port) <= 65535 or database in ('postgres', 'template0', 'template1')
            or any(not value or len(value.encode()) > limit or any(ord(c) < 32 or ord(c) == 127 for c in value)
                   for value, limit in ((user, 63), (database, 63), (password, 1024)))):
        raise ValueError()
except (ValueError, UnicodeError, IndexError, KeyError, OSError):
    sys.exit(invalid)

if action == 'configure':
    address = '[::1]' if host == '::1' else host
    username, secret, db = (url.quote(value, safe='') for value in (user, password, database))
    authority = f'{username}:{secret}@{address}:{int(port)}'
    dsn = url.urlunsplit(('postgres', authority, '/' + db, 'sslmode=' + sslmode, ''))
    config.write_text('NEKOPASS_DATABASE_URL=' + dsn + '\nNEKOPASS_TRUSTED_PROXIES=127.0.0.0/8,::1/128\n',
                      encoding='utf-8')
    sys.exit(0)

checks = """
SELECT CASE
 WHEN current_setting('server_version_num')::integer < 140000 THEN 'version'
 WHEN pg_is_in_recovery() OR current_setting('transaction_read_only')::boolean THEN 'readonly'
 WHEN NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = current_database()
                  AND datdba = (SELECT oid FROM pg_roles WHERE rolname = current_user)) THEN 'owner'
 WHEN current_schema() IS DISTINCT FROM 'public' OR NOT has_schema_privilege('public', 'CREATE') THEN 'schema'
 WHEN EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON c.relnamespace = n.oid
              WHERE n.nspname !~ '^pg_' AND n.nspname <> 'information_schema' AND c.relkind IN ('r','p','v','m','f'))
      AND (to_regclass('public.schema_version') IS NULL OR to_regclass('public.users') IS NULL
           OR to_regclass('public.site_settings') IS NULL) THEN 'shared'
 ELSE 'ok' END
"""
query = checks if action == 'check' else 'SELECT count(*) FROM public.users WHERE is_admin'
env = {key: value for key, value in os.environ.items() if not key.startswith('PG')}
env.update(PGHOST=host, PGPORT=port, PGDATABASE=database, PGUSER=user, PGSSLMODE=sslmode, PGCONNECT_TIMEOUT='5')
with tempfile.NamedTemporaryFile(mode='w', encoding='utf-8', dir=config.parent) as passfile:
    entry = (value.replace('\\', '\\\\').replace(':', '\\:') for value in (host, port, database, user, password))
    passfile.write(':'.join(entry) + '\n')
    passfile.flush()
    env['PGPASSFILE'] = passfile.name
    try:
        result = subprocess.run(['psql', '-w', '-XAtq', '-v', 'ON_ERROR_STOP=1', '-c', query],
                                text=True, capture_output=True, timeout=12, env=env, cwd='/')
    except (OSError, subprocess.SubprocessError):
        sys.exit('无法连接已有 PostgreSQL，请检查容器端口和连接信息')
value = result.stdout.strip()
if result.returncode:
    sys.exit('无法访问已有 PostgreSQL，请检查容器端口、数据库名和凭证')
if action == 'admins' and value.isascii() and value.isdigit():
    print(value)
elif action != 'check' or value != 'ok':
    sys.exit({
        'version': '已有数据库需使用 PostgreSQL 14 或以上',
        'readonly': '请选择可写的 PostgreSQL 主库',
        'owner': '连接用户必须是专用数据库的所有者',
        'schema': '数据库须使用 public schema，且连接用户需要建表权限',
        'shared': '该数据库已有其他表，请使用空的专用数据库或原 Nekopass 数据库',
    }.get(value, '无法确认已有数据库的状态'))
PY
}
prepare_external_database() (
 set +x
 if [[ -n "$DATABASE_URL_FILE" ]]; then
  external_postgres configure "$DATABASE_URL_FILE"
 else
  local database user password
  prompt DB_PORT '已有 PostgreSQL 的本机映射端口' "$DB_PORT"
  prompt database '已创建的专用数据库名' "$SERVICE"
  prompt user '数据库所有者用户名' "$SERVICE"
  printf '数据库密码（输入不回显）：' >&2
  if [[ -t 0 ]]; then IFS= read -rs password || die '交互输入已结束'
  else IFS= read -rs password </dev/tty || die '无法交互，请使用 --database-url-file'; fi
  printf '\n' >&2
  NEKOPASS_INSTALL_DB_PASSWORD=$password external_postgres configure '' "$DB_PORT" "$database" "$user"
 fi
 external_postgres check
 log '已核对本机 PostgreSQL，沿用指定的数据库'
)
log '请选择安装配置；回车使用默认值，可用方向键和删除键编辑。'
detected=$(hostname -I 2>/dev/null | awk '{print $1}')
prompt HOST '面板/节点可访问的域名或 IP' "${HOST:-${detected:-127.0.0.1}}"
prompt HTTP_PORT '面板 HTTP 端口' "$HTTP_PORT"
prompt GRPC_PORT '节点控制端口' "$GRPC_PORT"
prompt ADMIN '管理员账号' "$ADMIN"
prompt SITE '站点名称' "$SITE"
prompt TLS_MODE '节点连接（plain 测试明文 / existing 公共证书 / proxy 已有 TLS 代理）' "$TLS_MODE"
AGENT_HOST=${AGENT_HOST:-$HOST}
if [[ "$TLS_MODE" == proxy ]]; then
 prompt AGENT_HOST '现有代理的节点连接域名/IP' "$AGENT_HOST"
 prompt AGENT_PORT '现有代理的节点对外端口' "${AGENT_PORT:-443}"
else AGENT_PORT=${AGENT_PORT:-$GRPC_PORT}; fi
if [[ "$TLS_MODE" == existing ]]; then
 prompt TLS_CERT '已有证书链绝对路径' "$TLS_CERT"
 prompt TLS_KEY '已有私钥绝对路径' "$TLS_KEY"
fi
port_ok "$HTTP_PORT" && port_ok "$GRPC_PORT" && [[ "$HTTP_PORT" != "$GRPC_PORT" ]] || die '网页和节点端口须有效且不同'
port_ok "$AGENT_PORT" || die '节点对外端口不正确'
[[ "$PG_SOURCE" == system || "$PG_SOURCE" == pgdg ]] || die '数据库来源不正确'
[[ "$TLS_MODE" == plain || "$TLS_MODE" == existing || "$TLS_MODE" == proxy ]] || die '节点连接方式不正确'
export DEBIAN_FRONTEND=noninteractive
apt_indexes_ready=0
select_database
prepare_install_dependencies
python3 - "$HOST" "$AGENT_HOST" "$ADMIN" "$SITE" <<'PY'
import ipaddress,re,sys
host,agent,admin,site=sys.argv[1:]
for name in [host,agent]:
 try:ipaddress.ip_address(name)
 except ValueError:
  if len(name)>253 or not all(re.fullmatch(r'[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?',label) for label in name.split('.')):sys.exit('域名或 IP 不正确，不含协议、路径或端口')
if not admin or len(admin.encode())>64 or any(c in admin for c in '\r\n\0'):sys.exit('管理员账号不正确')
if not site.strip() or len(site)>64 or any(c in site for c in '\r\n\0'):sys.exit('站点名称不正确')
PY
WORK=$(mktemp -d /var/tmp/nekopass-panel-install.XXXXXXXX)
cleanup() {
 local status=$?
 if ((status)); then log "安装未完成，已有数据不会删除。查看：journalctl -u $SERVICE -n 50 --no-pager"; fi
 [[ -z "$WORK" || "$WORK" != /var/tmp/nekopass-panel-install.* ]] || rm -rf -- "$WORK"
}
trap cleanup EXIT
if ((external_database)); then prepare_external_database; fi
fetch() {
 python3 - "$1" <<'PY'
import sys,urllib.parse
v=urllib.parse.urlsplit(sys.argv[1])
if v.scheme!='https' or not v.hostname or v.username or v.password or v.fragment or any(c in sys.argv[1] for c in '\r\n\0'):sys.exit('下载地址必须为 HTTPS')
PY
 curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 600 --retry 3 "$1" -o "$2"
}
if [[ -z "$PACKAGE" && -z "$PACKAGE_URL" && -z "$DOWNLOAD_BASE" && -z "$SOURCE_DIR" ]]; then
 repo="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
 if [[ -f "$repo/dist/bin/nekopass" && -d "$repo/web/dist/pages" ]]; then SOURCE_DIR=$repo
 else prompt DOWNLOAD_BASE '面板版本下载根地址（HTTPS）' ''; fi
fi
if [[ -n "$SOURCE_DIR" ]]; then
 [[ -z "$PACKAGE" && -z "$PACKAGE_URL" ]] || die '源码目录与发布包不能同时使用'
 install -d "$WORK/release/bin" "$WORK/release/web"
 cp "$SOURCE_DIR/dist/bin/nekopass" "$WORK/release/bin/nekopass"
 cp -r "$SOURCE_DIR/web/dist/." "$WORK/release/web/"
 if [[ -f "$SOURCE_DIR/dist/bin/nekopassctl" ]]; then cp "$SOURCE_DIR/dist/bin/nekopassctl" "$WORK/release/bin/nekopassctl"; fi
else
 if [[ -z "$PACKAGE" ]]; then
  [[ -n "$PACKAGE_URL" ]] || PACKAGE_URL="$DOWNLOAD_BASE/$RELEASE_VERSION/nekopass-panel-linux-$ARCH.tar.gz"
  log '下载面板安装包'
  fetch "$PACKAGE_URL" "$WORK/panel.tar.gz"; PACKAGE="$WORK/panel.tar.gz"
 fi
 python3 - "$PACKAGE" "$WORK/release" <<'PY'
import pathlib,sys,tarfile
root=pathlib.Path(sys.argv[2]);root.mkdir()
with tarfile.open(sys.argv[1],'r:gz') as archive:
 members=archive.getmembers()
 if len(members)>20000 or sum(max(0,m.size) for m in members)>1024**3:sys.exit('安装包过大')
 for m in members:
  name=pathlib.PurePosixPath(m.name)
  if name.is_absolute() or '..' in name.parts or not name.parts or name.parts[0] not in {'bin','web','LICENSE'} or not (m.isfile() or m.isdir()):sys.exit('安装包路径不安全')
  if any(c in m.name for c in '\r\n\0\\'):sys.exit('安装包路径不正确')
 archive.extractall(root,filter='data') if sys.version_info>=(3,12) else archive.extractall(root)
PY
fi
[[ -f "$WORK/release/bin/nekopass" && -d "$WORK/release/web/pages" && -d "$WORK/release/web/assets" ]] || die '安装包缺少面板程序或完整网页'
chmod 700 "$WORK/release/bin/nekopass"
"$WORK/release/bin/nekopass" -h >/dev/null 2>&1 || die '面板程序无法在当前机器运行'
if ! write_manager_payload > "$WORK/nekopassctl"; then
 if [[ -f "$WORK/release/bin/nekopassctl" ]]; then cp "$WORK/release/bin/nekopassctl" "$WORK/nekopassctl"
 else companion="$(dirname -- "${BASH_SOURCE[0]}")/nekopassctl.sh"; [[ -f "$companion" ]] || die '缺少服务管理工具，请使用完整发布安装脚本'; tr -d '\r' < "$companion" > "$WORK/nekopassctl"; fi
fi
bash -n "$WORK/nekopassctl"
if ! write_updater_payload > "$WORK/nekopass-update"; then
 if [[ -f "$WORK/release/bin/nekopass-update" ]]; then cp "$WORK/release/bin/nekopass-update" "$WORK/nekopass-update"
 else companion="$(dirname -- "${BASH_SOURCE[0]}")/nekopass-update.py"; [[ -f "$companion" ]] || die '缺少更新工具，请使用完整发布安装脚本'; cp "$companion" "$WORK/nekopass-update"; fi
fi
if [[ "$TLS_MODE" == existing ]]; then
 [[ -f "$TLS_CERT" && -f "$TLS_KEY" ]] || die '节点连接证书或私钥不存在'
 openssl x509 -in "$TLS_CERT" -checkend 0 -noout >/dev/null || die '证书已过期或无效'
 openssl verify -untrusted "$TLS_CERT" "$TLS_CERT" >/dev/null || die '节点连接需要系统信任的公共 CA 证书链'
 openssl x509 -in "$TLS_CERT" -pubkey -noout > "$WORK/cert-public"
 openssl pkey -in "$TLS_KEY" -pubout > "$WORK/key-public" 2>/dev/null
 cmp -s "$WORK/cert-public" "$WORK/key-public" || die '证书与私钥不匹配'
 if python3 - "$AGENT_HOST" <<'PY'
import ipaddress,sys
try:ipaddress.ip_address(sys.argv[1])
except ValueError:sys.exit(1)
PY
 then openssl x509 -in "$TLS_CERT" -checkip "$AGENT_HOST" -noout >/dev/null || die '证书未包含连接 IP'
 else openssl x509 -in "$TLS_CERT" -checkhost "$AGENT_HOST" -noout >/dev/null || die '证书未包含连接域名'; fi
 install -m 644 "$TLS_CERT" "$WORK/imported.crt"
 install -m 600 "$TLS_KEY" "$WORK/imported.key"
fi
if [[ -n "$DOWNLOAD_BASE" && -z "$AGENT_RELEASES" ]]; then AGENT_RELEASES=$DOWNLOAD_BASE; fi
if [[ -n "$DOWNLOAD_BASE" && -z "$AGENT_INSTALLER" ]]; then AGENT_INSTALLER="${DOWNLOAD_BASE%/releases}/install-agent.sh"; fi
prompt AGENT_INSTALLER '可选节点安装脚本 HTTPS 地址（无下载源可留空）' "$AGENT_INSTALLER"
prompt AGENT_RELEASES '可选节点版本下载根地址（无下载源可留空）' "$AGENT_RELEASES"
python3 - "$HOST" "$HTTP_PORT" "$AGENT_HOST" "$AGENT_PORT" "$SITE" "$AGENT_INSTALLER" "$AGENT_RELEASES" "$RELEASE_VERSION" "$TLS_MODE" > "$WORK/settings.json" <<'PY'
import json,sys
host,http,agent,grpc,site,installer,releases,version,transport=sys.argv[1:]
import urllib.parse
for raw,base in [(installer,False),(releases,True)]:
 if not raw:continue
 u=urllib.parse.urlsplit(raw)
 if len(raw)>2048 or u.scheme!='https' or not u.hostname or u.username or u.password or u.fragment or base and u.query or any(c in raw for c in '\r\n\0'):sys.exit('节点安装文件地址须为有效 HTTPS URL')
authority='['+host+']' if ':' in host else host
print(json.dumps(dict(site_name=site,panel_url='http://'+authority+':'+http,agent_host=agent,agent_port=int(grpc),agent_transport='plain' if transport=='plain' else 'tls',installer_url=installer,release_base_url=releases,agent_version='latest',install_token_minutes=30)))
PY
configure_native_postgres() {
 if [[ -z "$selected_pg_cluster" ]]; then
  probe_port=${DB_PORT:-5432}
  if awk -v p="$probe_port" '$3==p {found=1} END {exit !found}' <<< "$initial_pg_clusters"; then
   [[ -z "$DB_PORT" ]] || die '指定端口上的 PostgreSQL 集群与所选版本不匹配或不可用，请检查集群'
  elif postgres_port_in_use "$probe_port"; then
   die "端口 $probe_port 已被系统集群列表之外的服务占用，无法自动沿用或新建 PostgreSQL"
  fi
  if [[ -z "$PG_VERSION" ]]; then
   installed=$(installed_postgres_versions)
   if [[ -n "$installed" ]]; then
    log "已安装的 PostgreSQL 版本：$(tr '\n' ' ' <<< "$installed")"
    prompt PG_VERSION 'PostgreSQL 主版本' "${installed##*$'\n'}"
    [[ "$PG_VERSION" =~ ^[0-9]{2}$ ]] && ((10#$PG_VERSION >= 14)) || die '数据库主版本须为 PostgreSQL 14 或以上'
   fi
  fi
  if [[ -z "$PG_VERSION" ]] || ! postgres_installed "$PG_VERSION"; then
   ((!SKIP_DEPS)) || die '未找到已安装的 PostgreSQL 14 或以上版本，请检查版本或允许安装依赖'
   prompt PG_SOURCE '数据库来源（system 系统源 / pgdg 官方源）' "$PG_SOURCE"
   [[ "$PG_SOURCE" == system || "$PG_SOURCE" == pgdg ]] || die '数据库来源不正确'
   if [[ "$PG_SOURCE" == pgdg ]]; then
    [[ "$VERSION_CODENAME" =~ ^[a-z]+$ ]] || die '系统发行代号不正确'
    # PostgreSQL official repository instructions: https://www.postgresql.org/download/linux/debian/
    fetch https://www.postgresql.org/media/keys/ACCC4CF8.asc "$WORK/pgdg.asc"
    install -d /usr/share/postgresql-common/pgdg
    install -m 644 "$WORK/pgdg.asc" /usr/share/postgresql-common/pgdg/nekopass.asc
    printf 'Types: deb\nURIs: https://apt.postgresql.org/pub/repos/apt\nSuites: %s-pgdg\nArchitectures: %s\nComponents: main\nSigned-By: /usr/share/postgresql-common/pgdg/nekopass.asc\n' "$VERSION_CODENAME" "$ARCH" > /etc/apt/sources.list.d/nekopass-pgdg.sources
    chmod 644 /etc/apt/sources.list.d/nekopass-pgdg.sources
    apt_indexes_ready=0
   fi
   ensure_apt_indexes
   available=$(apt-cache search --names-only '^postgresql-[0-9]+$' | awk '{sub("postgresql-", "", $1); if($1>=14) print $1}' | sort -n | tr '\n' ' ')
   [[ -n "$available" ]] || die '软件源没有 PostgreSQL 14 或以上版本'
   log "可用 PostgreSQL 版本：$available"
   if [[ -z "$PG_VERSION" ]]; then
    prompt PG_VERSION 'PostgreSQL 主版本' "$(awk '{print $NF}' <<< "$available")"
   fi
   [[ "$PG_VERSION" =~ ^[0-9]{2}$ && " $available " == *" $PG_VERSION "* ]] || die '选择的数据库版本不在可用列表中'
   apt-get install -y "postgresql-$PG_VERSION"
  fi
  postgres_installed "$PG_VERSION" || die '所选数据库版本尚未安装'
  pg_clusters=$(pg_lsclusters --no-header)
  # An APT install may have created a default cluster. Reuse it if it is
  # new, or if the user did not explicitly ask for a separate cluster.
  default_db=$(awk -v v="$PG_VERSION" -v separate="$new_pg_cluster" '
   NR == FNR { occupied[$3] = 1; next }
   $1 == v && ($4 == "online" || $4 == "down") && (!separate || !($3 in occupied)) { print $3; exit }
  ' <(printf '%s\n' "$initial_pg_clusters") <(printf '%s\n' "$pg_clusters"))
  if [[ -z "$default_db" ]]; then
   default_db=$(awk 'NF { occupied[$3] = 1 } END { port=5432; while (port in occupied) port++; print port }' <<< "$pg_clusters")
  fi
  prompt DB_PORT '本机数据库端口' "${DB_PORT:-$default_db}"
  port_ok "$DB_PORT" || die '数据库端口不正确'
  DB_PORT=$((10#$DB_PORT))
  if ((new_pg_cluster)) && awk -v p="$DB_PORT" '$3==p {found=1} END {exit !found}' <<< "$initial_pg_clusters"; then
   die '该端口已有 PostgreSQL 集群，请为新集群选择其他端口'
  fi
 fi
 pg_clusters=$(pg_lsclusters --no-header)
 cluster=$(awk -v v="$PG_VERSION" -v p="$DB_PORT" '$1==v && $3==p && ($4=="online" || $4=="down") {print $2;exit}' <<< "$pg_clusters")
 [[ -z "$selected_pg_cluster" || "$cluster" == "$selected_pg_cluster" ]] || die '所选 PostgreSQL 集群已变化，请重新运行安装器'
 if [[ -z "$cluster" ]]; then
  if awk -v p="$DB_PORT" '$3==p {found=1} END {exit !found}' <<< "$pg_clusters"; then die '该端口已有不可沿用的 PostgreSQL 集群，请选择其他端口'; fi
  if postgres_port_in_use "$DB_PORT"; then die "端口 $DB_PORT 已被占用，请为新集群选择其他端口"; fi
  cluster=$SERVICE
  if awk -v v="$PG_VERSION" -v c="$cluster" '$1==v && $2==c {found=1} END {exit !found}' <<< "$pg_clusters"; then cluster="$SERVICE-$DB_PORT"; fi
  pg_createcluster "$PG_VERSION" "$cluster" --port "$DB_PORT" --start
 else
  pg_ctlcluster "$PG_VERSION" "$cluster" start ||
   { pg_ctlcluster "$PG_VERSION" "$cluster" status >/dev/null && pg_isready -h /var/run/postgresql -p "$DB_PORT" >/dev/null; } ||
   die '所选 PostgreSQL 集群未在 /var/run/postgresql 就绪'
 fi
}
if ((!external_database)); then
 configure_native_postgres
 systemctl enable postgresql >/dev/null
 systemctl enable "postgresql@$PG_VERSION-$cluster" >/dev/null
 existing=$(postgres_run psql -p "$DB_PORT" -XAtqc "SELECT count(*) FROM pg_database WHERE datname='$SERVICE'")
 role=$(postgres_run psql -p "$DB_PORT" -XAtqc "SELECT count(*) FROM pg_roles WHERE rolname='$SERVICE'")
 if [[ "$existing" != 0 ]]; then
  owner=$(postgres_run psql -p "$DB_PORT" -XAtqc "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname='$SERVICE'")
  [[ "$owner" == "$SERVICE" ]] || die '同名数据库不属于该服务，无法继续安装；原数据库未修改'
  if ((!REINSTALL)); then log '复用已有数据库，保留用户、订单及配置数据'; fi
 fi
fi
id "$SERVICE" >/dev/null 2>&1 || useradd --system --user-group --home-dir /nonexistent --shell /usr/sbin/nologin "$SERVICE"
if ((REINSTALL)); then
 log "清空重装：服务 $SERVICE，PostgreSQL $PG_VERSION / 端口 $DB_PORT，数据库 $SERVICE。所有用户、余额、订单、节点和规则将删除。"
 if ((!YES)); then
  answer=''
  if [[ -t 0 ]]; then read -r -p "输入服务名 $SERVICE 确认清空：" answer || die '未确认清空'
  else read -r -p "输入服务名 $SERVICE 确认清空：" answer </dev/tty || die '无法交互确认，请使用 --reinstall --yes'; fi
  [[ "$answer" == "$SERVICE" ]] || die '已取消清空重装，数据库未修改'
 fi
 systemctl stop "$SERVICE-update.path" "$SERVICE-update.service" 2>/dev/null || true
fi
# Stop only the selected panel, after validating inputs and confirming reset.
if systemctl is-active --quiet "$SERVICE"; then systemctl stop "$SERVICE"; fi
python3 - "$HTTP_PORT" "$GRPC_PORT" "$TLS_MODE" <<'PY'
import socket,sys
for address,port in [('0.0.0.0',sys.argv[1]),('127.0.0.1' if sys.argv[3]=='proxy' else '0.0.0.0',sys.argv[2])]:
 with socket.socket() as sock:
  sock.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
  try:sock.bind((address,int(port)))
  except OSError:sys.exit('端口 '+port+' 已被占用，请选择其他端口')
PY
if ((REINSTALL)); then
 if [[ "$existing" != 0 ]]; then postgres_run dropdb -p "$DB_PORT" --force "$SERVICE"; existing=0; fi
 # Keep the OS/PostgreSQL role and co-located Agent files. Clear panel state only.
 for target in "$BASE/web" "$CONFIG/tls" "$STATE"; do
  [[ ! -L "$target" && "$(realpath -m "$target")" == "$target" ]] || die '清空目标路径不正确'
  [[ ! -e "$target" ]] || rm -rf -- "$target"
 done
 rm -f -- "$CONFIG/control.env"
fi
if ((!external_database)); then
 if [[ "$role" == 0 ]]; then postgres_run createuser -p "$DB_PORT" "$SERVICE"; fi
 if [[ "$existing" == 0 ]]; then postgres_run createdb -p "$DB_PORT" -O "$SERVICE" "$SERVICE"; fi
fi
install -d -m 755 "$BASE/bin" "$BASE/web" "$CONFIG"
install -m 755 "$WORK/release/bin/nekopass" "$BASE/bin/nekopass"
cp -r "$WORK/release/web/." "$BASE/web/"
# Installation is private by default; published web files must be readable by the service.
find "$BASE/web" -type d -exec chmod 755 {} +
find "$BASE/web" -type f -exec chmod 644 {} +
if ((!external_database)); then
 python3 - "$SERVICE" "$DB_PORT" "$WORK/control.env" <<'PY'
import pathlib,sys,urllib.parse
name,port,path=sys.argv[1:]
dsn='postgres://'+urllib.parse.quote(name,safe='')+'@/'+urllib.parse.quote(name,safe='')+'?host=/var/run/postgresql&port='+port+'&sslmode=disable'
pathlib.Path(path).write_text('NEKOPASS_DATABASE_URL='+dsn+'\nNEKOPASS_TRUSTED_PROXIES=127.0.0.0/8,::1/128\n')
PY
fi
GRPC_BIND=0.0.0.0
if [[ "$TLS_MODE" == existing ]]; then
 install -d -m 750 -g "$SERVICE" "$CONFIG/tls"
 install -m 644 "$WORK/imported.crt" "$CONFIG/tls/server.crt"
 install -m 640 -g "$SERVICE" "$WORK/imported.key" "$CONFIG/tls/server.key"
 printf 'NEKOPASS_GRPC_TLS_CERT=%s/tls/server.crt\nNEKOPASS_GRPC_TLS_KEY=%s/tls/server.key\n' "$CONFIG" "$CONFIG" >> "$WORK/control.env"
elif [[ "$TLS_MODE" == proxy ]]; then GRPC_BIND=127.0.0.1
else GRPC_BIND=0.0.0.0; fi
install -m 600 "$WORK/control.env" "$CONFIG/control.env"
postgres_after=''
postgres_requires=''
if ((!external_database)); then
 postgres_after=" postgresql@$PG_VERSION-$cluster.service"
 postgres_requires="Requires=postgresql@$PG_VERSION-$cluster.service"
fi
cat > "$WORK/panel.service" <<UNIT
[Unit]
Description=Nekopass control panel
After=network-online.target$postgres_after
Wants=network-online.target
$postgres_requires
[Service]
Type=simple
User=$SERVICE
Group=$SERVICE
EnvironmentFile=$CONFIG/control.env
ExecStart=$BASE/bin/nekopass -web $BASE/web -http :$HTTP_PORT -grpc $GRPC_BIND:$GRPC_PORT
Restart=on-failure
RestartSec=3
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
LimitNOFILE=65536
[Install]
WantedBy=multi-user.target
UNIT
install -m 644 "$WORK/panel.service" "$UNIT"
install -d -m 755 /usr/local/bin
install_shared_tool "$WORK/nekopassctl" /usr/local/bin/nekopassctl
install_shared_tool "$WORK/nekopass-update" "$BASE/bin/nekopass-update"
"$BASE/bin/nekopass-update" --service "$SERVICE" --setup-panel
run_panel() { systemd-run --quiet --wait --pipe --collect -p "User=$SERVICE" -p "EnvironmentFile=$CONFIG/control.env" -- "$BASE/bin/nekopass" "$@"; }
run_panel -mode migrate
run_panel -mode init-settings < "$WORK/settings.json"
if ((external_database)); then
 admins=$(external_postgres admins)
else
 admins=$(postgres_run psql -p "$DB_PORT" -d "$SERVICE" -XAtqc "SELECT count(*) FROM users WHERE is_admin")
fi
if [[ "$admins" == 0 ]]; then
 log '创建管理员并随机生成密码'
 run_panel -mode bootstrap -random-password -admin-name "$ADMIN" > "$WORK/admin-result"
else
 printf '已有管理员账号及密码已保留；忘记密码请使用 nekopassctl 重新生成管理员密码。\n' > "$WORK/admin-result"
fi
systemctl daemon-reload
systemctl enable --now "$SERVICE" >/dev/null
ready=0
for ((i=0;i<30;i++)); do if curl --fail --silent "http://127.0.0.1:$HTTP_PORT/healthz" >/dev/null; then ready=1; break; fi; sleep 1; done
((ready)) && systemctl is-active --quiet "$SERVICE" || die '面板未能正常启动，请查看日志'
if [[ "$FIREWALL" == auto ]]; then
 ports=("$HTTP_PORT"); [[ "$TLS_MODE" == proxy ]] || ports+=("$GRPC_PORT")
 if command -v ufw >/dev/null && LC_ALL=C ufw status | grep -q '^Status: active'; then
  for port in "${ports[@]}"; do ufw allow "$port/tcp"; done
 elif command -v firewall-cmd >/dev/null && firewall-cmd --state >/dev/null 2>&1; then
  for port in "${ports[@]}"; do firewall-cmd --permanent --add-port="$port/tcp"; firewall-cmd --add-port="$port/tcp"; done
 fi
fi
log "安装完成，面板及数据库已设置开机自启。"
authority=$HOST; [[ "$HOST" != *:* ]] || authority="[$HOST]"
printf '面板地址：http://%s:%s\n服务菜单：nekopassctl\n重置密码：nekopassctl %s reset-password\n' "$authority" "$HTTP_PORT" "$([[ "$SERVICE" == nekopass ]] && echo panel || echo "$SERVICE")"
cat "$WORK/admin-result"
if [[ "$TLS_MODE" == proxy ]]; then log "已选择现有代理模式，请确保外部 TLS 入口代理到 127.0.0.1:$GRPC_PORT。"; fi
log "使用云服务器时，还需在云平台安全组放行网页 $HTTP_PORT 和节点连接 $AGENT_PORT。"
