#!/bin/bash
# 编译 mod 并装到游戏 mods 目录。
set -e
set -o pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
GAME="${STS2_GAME:-$HOME/Games/steamLib/steamapps/common/Slay the Spire 2}"
MODID=sts2textbridge

cd "$HERE"
dotnet build "/p:Sts2Path=$GAME" -v quiet | tail -5

DLL="$HERE/.godot/mono/temp/bin/Debug/$MODID.dll"
[ -f "$DLL" ] || { echo "!! 找不到 $DLL"; exit 1; }

DEST="$GAME/mods/$MODID"
mkdir -p "$DEST"
cp "$DLL" "$DEST/"
cp "$HERE/$MODID.json" "$DEST/"
echo "installed -> $DEST"
ls -l "$DEST"
