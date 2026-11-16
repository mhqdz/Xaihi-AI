#!/bin/bash
# 启动游戏（带桥）。
#   用法: ./run.sh [--restart] [数据目录]
#   数据目录给了就用它当 XDG_DATA_HOME（干净存档，不碰真实存档）。
set -u
GAME="${STS2_GAME:-$HOME/Games/steamLib/steamapps/common/Slay the Spire 2}"
ARG1="${1:-}"
DATADIR="${2:-}"
export SteamAppId="${STS2_APPID:-2868840}"
export STS2_BRIDGE=1
if [ -n "$DATADIR" ]; then export XDG_DATA_HOME="$DATADIR"; fi
if [ "$ARG1" = "--restart" ]; then pkill -x SlayTheSpire2 || true; sleep 3; fi
cd "$GAME" || exit 1
setsid nohup ./SlayTheSpire2 --rendering-driver opengl3 > /tmp/sts2-run.log 2>&1 < /dev/null &
echo "launched data=${XDG_DATA_HOME:-$HOME/.local/share}"
