#!/bin/bash
# probe 的壳：默认用真实数据目录（游戏正常启动时的桥目录）
export STS2_BRIDGE_DATA="${BRIDGE_DATA:-$HOME/.local/share/slaythespire2/bridge}"
exec python3 "$(dirname "$0")/probe.py" "$@"
