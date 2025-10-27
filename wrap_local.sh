#!/usr/bin/env bash
set -e

# unshare: Run a program with some namespaces unshared from the parent.
unshare \
    -Ufpm \
    # -U: Create a new user namespace.
    # -f: Fork a new process before executing the command (required for -p).
    # -p: Create a new PID namespace.
    # -m: Create a new mount namespace.
    \
    --kill-child \
    # Ensure the forked child process is terminated when the main process exits.
    \
    --map-user="$(id -u)" \
    # Map the current user's ID to the same ID inside the new user namespace.
    \
    --map-group="$(id -g)" \
    # Map the current user's group ID to the same ID inside the new user namespace.
    \
    --mount-proc \
    # Mount a new /proc filesystem for the new mount namespace to see correct process info.
    \
    "$@"
    # Pass all arguments given to this script along to the unshare command.


