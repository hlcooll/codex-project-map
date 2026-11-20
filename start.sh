#!/usr/bin/env bash
set -euo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
workspace="$project_dir"
data_dir="$project_dir/.data"
port=4399
rebuild=0
open_browser=1
server_pid=''
build_tmp=''

usage() {
  cat <<'HELP'
用法：./start.sh [选项]
  --port PORT        本机端口，默认 4399
  --workspace PATH   启动时登记的项目目录，默认工作台目录
  --data PATH        数据目录，默认工作台的 .data
  --rebuild          重新安装锁定依赖并构建前后端
  --no-open          不自动打开浏览器
  -h, --help         显示帮助

可用 NODE_BIN、PNPM_BIN、GO_BIN、CODEX_BIN 指定工具的绝对路径。
前台运行，按 Ctrl+C 停止。支持 macOS 和 Linux，需要 Bash 与 curl。
HELP
}
fail() { printf '启动失败：%s\n' "$*" >&2; exit 1; }
while (($#)); do
  case "$1" in
    --port|--workspace|--data)
      (($# >= 2)) && [[ -n "$2" && "$2" != --* ]] || fail "$1 缺少参数"
      case "$1" in
        --port) port="$2" ;;
        --workspace) workspace="$2" ;;
        --data) data_dir="$2" ;;
      esac
      shift 2 ;;
    --rebuild) rebuild=1; shift ;;
    --no-open) open_browser=0; shift ;;
    -h|--help) usage; exit 0 ;;
    *) fail "未知选项 $1；用 --help 查看用法" ;;
  esac
done
[[ "$port" =~ ^[0-9]{1,5}$ ]] || fail '端口须为 1–65535 的整数'
port=$((10#$port))
((port >= 1 && port <= 65535)) || fail '端口须为 1–65535 的整数'
[[ -d "$workspace" ]] || fail "项目目录不存在：$workspace"
workspace="$(cd -- "$workspace" && pwd -P)"
command -v curl >/dev/null 2>&1 || fail '未找到 curl，请安装后重试'
url="http://127.0.0.1:$port"
if ( : > /dev/tcp/127.0.0.1/"$port") 2>/dev/null; then
  fail "端口 $port 已占用，请确认 $url 的服务，或选择其他端口。"
fi

# One launcher owns this checkout's build outputs and running process at a time.
mkdir -p "$project_dir/.data"
lock_dir="$project_dir/.data/start.lock"
mkdir "$lock_dir" 2>/dev/null || fail "此目录已有启动进程；若上次被强制终止，请确认服务已停止后移除 ${lock_dir}。"
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if [[ -n "$server_pid" ]]; then
    kill -TERM "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  [[ -z "$build_tmp" ]] || rm -f -- "$build_tmp"
  rmdir "$lock_dir" 2>/dev/null || true
  return "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir -p "$data_dir"
data_dir="$(cd -- "$data_dir" && pwd -P)"
cd -- "$project_dir"

needs_build() {
  local output="$1" input changed
  shift
  [[ "$rebuild" == 0 && -f "$output" ]] || return 0
  for input in "$@"; do
    [[ -e "$input" ]] || return 0
    # Include directories so removal of a source file also invalidates the build.
    changed="$(find "$input" -newer "$output" -print -quit)" || fail "无法检查构建输入：$input"
    [[ -z "$changed" ]] || return 0
  done
  return 1
}
resolve_tool() {
  local requested="$1" resolved
  resolved="$(command -v "$requested" 2>/dev/null)" || return 1
  [[ -x "$resolved" ]] || return 1
  # Keep resolved executables usable after changing into server/.
  case "$resolved" in
    /*) printf '%s\n' "$resolved" ;;
    *) printf '%s/%s\n' "$project_dir" "$resolved" ;;
  esac
}

frontend_inputs=(src index.html package.json pnpm-lock.yaml pnpm-workspace.yaml vite.config.ts tsconfig.json)
[[ ! -d public ]] || frontend_inputs+=(public)
if [[ ! -d dist/assets ]] || needs_build dist/index.html "${frontend_inputs[@]}"; then
  node_bin="$(resolve_tool "${NODE_BIN:-node}")" || fail '未找到 Node.js 22.12+，请安装或设置 NODE_BIN'
  "$node_bin" -e 'const [a,b]=process.versions.node.split(".").map(Number); process.exit(a>22 || a===22&&b>=12 ? 0 : 1)' || fail '需要 Node.js 22.12 或以上版本'
  export PATH="$(dirname -- "$node_bin"):$PATH"
  pnpm_bin="$(resolve_tool "${PNPM_BIN:-pnpm}")" || fail '未找到 pnpm，请安装或设置 PNPM_BIN'
  printf '安装锁定的前端依赖并构建网页…\n'
  "$pnpm_bin" install --frozen-lockfile
  "$pnpm_bin" build
else
  printf '前端源码未变化，使用已有构建。\n'
fi

if [[ ! -x bin/project-map ]] || needs_build bin/project-map server; then
  go_bin="$(resolve_tool "${GO_BIN:-go}")" || fail '未找到 Go 1.24+，请安装或设置 GO_BIN；SQLite 构建还需要 C 编译器'
  printf '构建 Go 服务…\n'
  mkdir -p bin
  build_tmp="$(mktemp "$project_dir/bin/.project-map.XXXXXX")"
  (cd server && CGO_ENABLED=1 "$go_bin" build -o "$build_tmp" .)
  mv -f -- "$build_tmp" "$project_dir/bin/project-map"
  build_tmp=''
else
  printf '后端源码未变化，使用已有构建。\n'
fi

codex_bin='codex'
if [[ -n "${CODEX_BIN:-}" ]]; then
  codex_bin="$(resolve_tool "$CODEX_BIN")" || fail 'CODEX_BIN 不是可执行文件'
elif resolved_codex="$(resolve_tool codex)"; then
  codex_bin="$resolved_codex"
else
  # Desktop installations may not expose the bundled CLI on PATH.
  for candidate in /Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex /Applications/Codex.app/Contents/Resources/codex; do
    if [[ -x "$candidate" ]]; then codex_bin="$candidate"; break; fi
  done
  if [[ "$codex_bin" == codex ]]; then
    printf '未找到 Codex CLI：可以浏览项目；发起分析和开发任务前请安装并登录 Codex。\n'
  fi
fi

"$project_dir/bin/project-map" -addr "127.0.0.1:$port" -workspace "$workspace" \
  -analysis-workspace "$project_dir" -web "$project_dir/dist" -data "$data_dir" -codex-bin "$codex_bin" &
server_pid=$!
ready=0
for ((attempt=0; attempt<30; attempt++)); do
  kill -0 "$server_pid" 2>/dev/null || { wait "$server_pid" || true; fail '服务提前退出，请检查上方日志'; }
  if [[ "$(curl --noproxy '*' --silent --fail --max-time 1 "$url/api/health" 2>/dev/null || true)" == ok ]]; then ready=1; break; fi
  sleep 0.2
done
[[ "$ready" == 1 ]] || fail '服务未在预期时间内就绪，请检查上方日志'
printf '\n工作台已启动：%s\n按 Ctrl+C 停止。\n' "$url"
if [[ "$open_browser" == 1 ]]; then
  if command -v open >/dev/null 2>&1 && [[ "$(uname -s)" == Darwin ]]; then
    open "$url" >/dev/null 2>&1 || true
  elif command -v xdg-open >/dev/null 2>&1; then
    xdg-open "$url" >/dev/null 2>&1 || true
  fi
fi
wait "$server_pid"
