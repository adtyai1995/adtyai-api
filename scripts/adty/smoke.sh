#!/usr/bin/env bash
# 方案 F.1.1 的 S-01—S-10；运行前准备隔离服务及合成 Root。
# ADTY_SMOKE_* 的必填项和证据目录见 smoke.py --help。
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."
case "$(pwd -P)" in
  /mnt | /mnt/*) echo 'smoke: 只能在 WSL 原生目录的源码副本运行' >&2; exit 2 ;;
esac
exec python3 scripts/adty/smoke.py "$@"
