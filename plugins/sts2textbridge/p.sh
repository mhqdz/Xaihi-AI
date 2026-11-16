#!/bin/bash
# probe 的壳：默认用真实数据目录；已设 STS2_BRIDGE_DATA / BRIDGE_DATA 就听它们的。
export STS2_BRIDGE_DATA="${STS2_BRIDGE_DATA:-${BRIDGE_DATA:-$HOME/.local/share/slaythespire2/bridge}}"
exec python3 "$(dirname "$0")/probe.py" "$@"
