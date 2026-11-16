#!/bin/bash
# 启动游戏（带桥）。
#   用法: ./run.sh [数据目录] [--restart]
#   给了数据目录就用它当 XDG_DATA_HOME（干净存档，不碰真实存档）。
#   换过 appid 又没给数据目录 → 直接拒绝（会污染真实存档，见 2026-11-17 丢档事故）。
set -u
GAME="${STS2_GAME:-$HOME/Games/steamLib/steamapps/common/Slay the Spire 2}"
REAL_APPID=2868840
DATADIR=""
RESTART=0
for a in "$@"; do
  case "$a" in
    --restart) RESTART=1 ;;
    *) DATADIR="$a" ;;
  esac
done
export SteamAppId="${STS2_APPID:-$REAL_APPID}"
export STS2_BRIDGE="${STS2_BRIDGE-1}"
if [ -n "$DATADIR" ]; then
  export XDG_DATA_HOME="$DATADIR"
elif [ "$SteamAppId" != "$REAL_APPID" ]; then
  echo "!! appid=$SteamAppId 但没给数据目录；这会覆盖真实存档。用法: ./run.sh <数据目录>" >&2
  exit 2
fi
if [ "$RESTART" = 1 ]; then pkill -x SlayTheSpire2 || true; sleep 3; fi
cd "$GAME" || exit 1
setsid nohup ./SlayTheSpire2 --rendering-driver opengl3 > /tmp/sts2-run.log 2>&1 < /dev/null &
echo "launched appid=$SteamAppId data=${XDG_DATA_HOME:-$HOME/.local/share}"
