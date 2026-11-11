#!/bin/sh
set -eu
# Buf v1.47.2; baseline is the first stable daemon v2 schema.
buf breaking --against internal/daemon/wire/baseline.binpb
# Also compare the released schema supplied by CI, so later additions stay stable.
if [ "${1:-}" != "" ]; then
  buf breaking --against "$1"
fi
