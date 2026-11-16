#!/bin/bash
# 写一个动作。用法: ./act.sh '{"action":"click","index":0}'
D="${STS2_BRIDGE_DATA:-${XDG_DATA_HOME:-$HOME/.local/share}/slaythespire2/bridge}"
mkdir -p "$D"
printf '%s' "$1" > "$D/action.json.tmp" && mv -f "$D/action.json.tmp" "$D/action.json"
