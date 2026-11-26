#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
BIN_DIR="${MAW_BIN_DIR:-${HOME}/.local/bin}"
WEB_DIR="$ROOT/internal/board/web"

# 可选开关：
#   MAW_SKIP_WEB=1  跳过 board-web 构建（离线/CI 冒烟用；页面资源仍用仓库里已构建的 dist）
SKIP_WEB="${MAW_SKIP_WEB:-0}"

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

echo "Ensure $BIN_DIR is on PATH"
"$BIN_DIR/maw" --version
