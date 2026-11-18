#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
BIN_DIR="${MAW_BIN_DIR:-${HOME}/.local/bin}"
WEB_DIR="$ROOT/internal/board/web"
PI_DIR="$ROOT/runtimes/pi"
MIN_NODE="22.19.0"

# 可选开关：
#   MAW_SKIP_WEB=1  跳过 board-web 构建（离线/CI 冒烟用；页面资源仍用仓库里已构建的 dist）
#   MAW_SKIP_PI=1   只装 maw 本体，不装 Pi 运行时（明确知道不用 Pi 的场景）
SKIP_WEB="${MAW_SKIP_WEB:-0}"
SKIP_PI="${MAW_SKIP_PI:-0}"

require_node() {
  if ! command -v node >/dev/null 2>&1; then
    cat >&2 <<'ERR'
error: 未找到 node。Pi 运行时需要 Node >= 22.19.0。
  macOS:  brew install node@22        或   nvm install 22 && nvm use 22
  Ubuntu: 用 NodeSource 安装 22.x，或 nvm install 22
只装 maw 本体（不带 Pi 运行时）：MAW_SKIP_PI=1 ./install.sh
ERR
    exit 1
  fi
  local version
  version="$(node -p 'process.versions.node')"
  if ! node -e '
    const need = process.argv[1].split(".").map(Number);
    const got = process.versions.node.split(".").map(Number);
    process.exit(got[0] > need[0] || (got[0] === need[0] && got[1] >= need[1]) ? 0 : 1);
  ' "$MIN_NODE"; then
    cat >&2 <<ERR
error: Node $version 太旧，Pi 运行时要求 >= $MIN_NODE。
  nvm install 22 && nvm use 22
只装 maw 本体（不带 Pi 运行时）：MAW_SKIP_PI=1 ./install.sh
ERR
    exit 1
  fi
}

install_pi_runtime() {
  if [[ ! -f "$PI_DIR/package-lock.json" ]]; then
    echo "error: 缺少 $PI_DIR/package-lock.json：发布包不完整，无法精确安装固定版本的 Pi SDK" >&2
    exit 1
  fi
  echo "Installing Pi runtime dependencies (pinned, from package-lock.json)..."
  npm ci --prefix "$PI_DIR" --omit=dev --no-audit --no-fund
  echo "Probing Pi runtime (no model key required)..."
  local report
  report="$(node "$PI_DIR/src/main.mjs" --probe)"
  node -e '
    const r = JSON.parse(process.argv[1]);
    if (!r.ok) { console.error("error: Pi 运行时自报 ok=false"); process.exit(1); }
    if (r.protocol.format !== "pi-stream-json" || r.protocol.version !== "1") {
      console.error(`error: 协议不匹配 ${r.protocol.format}/${r.protocol.version}，期望 pi-stream-json/1`);
      process.exit(1);
    }
    console.log(`Pi runtime ready: sdk=${r.sdk} node=${r.node} protocol=${r.protocol.format}/${r.protocol.version}`);
  ' "$report"
}

# 先验证 Pi 运行时依赖，再做耗时构建：缺 node 时应当立刻给出可操作错误。
if [[ "$SKIP_PI" != "1" ]]; then
  require_node
fi

mkdir -p "$BIN_DIR"

if [[ "$SKIP_WEB" != "1" ]]; then
  echo "Building board-web (npm)..."
  if [[ ! -d "$WEB_DIR/node_modules" ]]; then
    npm ci --prefix "$WEB_DIR"
  fi
  npm run build --prefix "$WEB_DIR"
  test -f "$ROOT/internal/board/assets/dist/board-web.js"
else
  echo "Skipping board-web build (MAW_SKIP_WEB=1)"
fi

echo "Building maw (CGO_ENABLED=0 single binary)..."
rm -f "$BIN_DIR/maw"
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$BIN_DIR/maw" "$ROOT/cmd/maw"
echo "Installed: $BIN_DIR/maw"

if [[ "$SKIP_PI" == "1" ]]; then
  echo "Skipping Pi runtime (MAW_SKIP_PI=1)"
else
  install_pi_runtime
fi

echo "Ensure $BIN_DIR is on PATH"
"$BIN_DIR/maw" --version
