#!/usr/bin/bash
set -e
SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"

function run_in_namespace() {
  unshare -U --map-user="$(id -u)" --map-group="$(id -g)" -m --mount-proc -f -p --kill-child "$@"
}
run_in_namespace "$@"
