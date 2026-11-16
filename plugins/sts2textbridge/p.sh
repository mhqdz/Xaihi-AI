#!/bin/bash
# probe 的壳：默认指向隔离测试目录
export STS2_BRIDGE_DATA="${BRIDGE_DATA:-/tmp/sts2test/slaythespire2/bridge}"
exec python3 "$(dirname "$0")/probe.py" "$@"
