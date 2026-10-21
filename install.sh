#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
BIN="${HOME}/.local/bin"
mkdir -p "$BIN"
cat >"$BIN/maw" <<EOF
#!/usr/bin/env bash
export PYTHONPATH="${ROOT}/src:\${PYTHONPATH:-}"
exec python3 -m maw "\$@"
EOF
chmod +x "$BIN/maw"
echo "Installed: $BIN/maw"
echo "Ensure $BIN is on PATH"
"$BIN/maw" --version
