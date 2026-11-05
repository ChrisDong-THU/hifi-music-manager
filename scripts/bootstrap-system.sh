#!/bin/bash
# Enable this user's services after logout. No system-wide USB rules or fstab edits.
set -euo pipefail
test "$(id -u)" -eq 0 || { echo 'Run with sudo'; exit 1; }
service_user="${1:-${SUDO_USER:-}}"
test -n "$service_user" && test "$service_user" != root || { echo 'Usage: sudo bash bootstrap-system.sh SERVICE_USER'; exit 1; }
id "$service_user" >/dev/null
loginctl enable-linger "$service_user"
echo "READY: $service_user user services may run after logout; USB policy and fstab unchanged."
