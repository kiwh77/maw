#!/usr/bin/env bash
# Controlled CodeBuddy stand-in for tests/managed.
# Labeled source: synthetic fixture (NOT a vendor field measurement).
# Real CLI smoke is opt-in via MAW_MANAGED_SMOKE=1 (see README.md).
set -euo pipefail

case "${1:-}" in
  --version)
    echo "2.173.0"
    exit 0
    ;;
  --help)
    cat <<'EOF'
Usage: codebuddy|cbc [options] [command] [prompt]
Options:
  --version
  --help
  -p, --print
  --output-format <format>
  --permission-mode <mode>
  --dangerously-skip-permissions
EOF
    exit 0
    ;;
esac

# Task run path: emit a minimal stream-json sample then exit.
# MAW_FAKE_CB_EXIT overrides exit code; MAW_FAKE_CB_MODE=empty emits no result record.
mode="${MAW_FAKE_CB_MODE:-ok}"
if [[ "$mode" == "empty" ]]; then
  echo '{"type":"system","subtype":"init","session_id":"sess-test"}'
  exit "${MAW_FAKE_CB_EXIT:-0}"
fi

cat <<'EOF'
{"type":"system","subtype":"init","session_id":"sess-test","cwd":"/tmp","model":"fake"}
{"type":"result","subtype":"success","is_error":false,"result":"managed-loop-ok","session_id":"sess-test","duration_ms":1,"num_turns":1}
EOF
exit "${MAW_FAKE_CB_EXIT:-0}"
