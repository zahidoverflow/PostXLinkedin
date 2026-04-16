#!/usr/bin/env bash
set -euo pipefail

APP_NAME="postxlinkedinbot"
APP_DIR="/opt/PostXLinkedin/PostXLinkedInbot"
SERVICE_NAME="postxlinkedinbot.service"

if [[ "${EUID}" -ne 0 ]]; then
  echo "Run as root: sudo ./scripts/vps/update.sh"
  exit 1
fi

mkdir -p "${APP_DIR}"
rsync -a --delete ./ "${APP_DIR}/"

cd "${APP_DIR}"
go build -o "${APP_NAME}" ./cmd/postxlinkedinbot

systemctl restart "${SERVICE_NAME}"
echo "Updated and restarted ${SERVICE_NAME}."

