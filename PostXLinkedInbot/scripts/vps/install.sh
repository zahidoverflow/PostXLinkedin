#!/usr/bin/env bash
set -euo pipefail

# Simple VPS installer for Ubuntu/Debian-like systems.
# Assumes you cloned the repo and are running from PostXLinkedInbot/.

APP_NAME="postxlinkedinbot"
APP_DIR="/opt/PostXLinkedin/PostXLinkedInbot"
SERVICE_NAME="postxlinkedinbot.service"

if [[ "${EUID}" -ne 0 ]]; then
  echo "Run as root: sudo ./scripts/vps/install.sh"
  exit 1
fi

mkdir -p "${APP_DIR}"
rsync -a --delete ./ "${APP_DIR}/"

cd "${APP_DIR}"

if ! command -v go >/dev/null 2>&1; then
  echo "Go is required on the VPS. Install Go, then rerun."
  exit 1
fi

go build -o "${APP_NAME}" ./cmd/postxlinkedinbot

if [[ ! -f "${APP_DIR}/.env" ]]; then
  cp .env.example .env
  chmod 600 .env
  echo "Created ${APP_DIR}/.env (edit it)."
fi

install -m 0644 -D scripts/systemd/postxlinkedinbot.service "/etc/systemd/system/${SERVICE_NAME}"

systemctl daemon-reload
systemctl enable "${SERVICE_NAME}"

echo "Installed systemd service ${SERVICE_NAME}."
echo "Next:"
echo "1) Edit ${APP_DIR}/.env"
echo "2) Start: systemctl start ${SERVICE_NAME}"
echo "3) Logs: journalctl -u ${SERVICE_NAME} -f"

