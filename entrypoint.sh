#!/bin/sh
set -e

DATA_DIR="${DATA_DIR:-/data}"

if [ ! -w "$DATA_DIR" ]; then
  echo "Fixing permissions on $DATA_DIR..."
  chmod 777 "$DATA_DIR"
fi

exec su-exec clipshot "$@"
