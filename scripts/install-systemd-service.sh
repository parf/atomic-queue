#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
unit_src="$repo_dir/systemd/atomic-queue.service"
user_unit_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
user_unit_path="$user_unit_dir/atomic-queue.service"
binary_path="$repo_dir/atomic-queue"
socket_path="${ATOMIC_QUEUE_SOCKET:-${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/atomic-queue/atomic-queue.sock}"

mkdir -p "$user_unit_dir"

if [[ ! -x "$binary_path" ]]; then
  echo "building atomic-queue binary"
  (cd "$repo_dir" && go build -o atomic-queue .)
fi

install -m 0644 "$unit_src" "$user_unit_path"

systemctl --user daemon-reload
systemctl --user enable --now atomic-queue.service

cat <<EOF
Installed user unit: $user_unit_path
Started service: atomic-queue.service

Client socket for the service:
  $socket_path

Use it like this:
  export ATOMIC_QUEUE_SOCKET="$socket_path"
  atomic-queue --run push jobs 'hello'
  atomic-queue --run pop jobs --timeout 5s

Useful commands:
  systemctl --user status atomic-queue.service
  systemctl --user restart atomic-queue.service
  journalctl --user -u atomic-queue.service -f
EOF
