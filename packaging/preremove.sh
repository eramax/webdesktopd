#!/bin/sh
set -e

# Stop and disable the service before removal.
if command -v systemctl > /dev/null 2>&1 && [ -d /run/systemd/system ]; then
    systemctl stop webdesktopd.service 2>/dev/null || true
    systemctl disable webdesktopd.service 2>/dev/null || true
fi

if command -v rc-service > /dev/null 2>&1; then
    rc-service webdesktopd stop 2>/dev/null || true
    rc-update del webdesktopd 2>/dev/null || true
fi
