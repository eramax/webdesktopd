#!/bin/sh
set -e

# Create the webdesktopd system group and user if they don't exist yet.
if command -v groupadd > /dev/null 2>&1; then
    # Debian / systemd systems
    getent group webdesktopd > /dev/null 2>&1 || \
        groupadd --system webdesktopd
    id -u webdesktopd > /dev/null 2>&1 || \
        useradd --system --no-create-home --shell /sbin/nologin \
            --gid webdesktopd --comment "webdesktopd daemon" webdesktopd
elif command -v addgroup > /dev/null 2>&1; then
    # Alpine / BusyBox — group must exist before adduser -G
    getent group webdesktopd > /dev/null 2>&1 || \
        addgroup -S webdesktopd
    id -u webdesktopd > /dev/null 2>&1 || \
        adduser -S -H -s /sbin/nologin -G webdesktopd \
            -g "webdesktopd daemon" webdesktopd
fi

# Tighten config directory ownership (created by package at 0750,
# now assign the group so the daemon can read it).
chown root:webdesktopd /etc/webdesktopd || true
chmod 750 /etc/webdesktopd || true
if [ -f /etc/webdesktopd/env ]; then
    chown root:webdesktopd /etc/webdesktopd/env || true
    chmod 640 /etc/webdesktopd/env || true
fi

# Debian / systemd
if command -v systemctl > /dev/null 2>&1 && [ -d /run/systemd/system ]; then
    systemctl daemon-reload
    systemctl enable webdesktopd.service
    echo "Run 'systemctl start webdesktopd' to start the service."
fi

# Alpine / OpenRC
if command -v rc-update > /dev/null 2>&1; then
    rc-update add webdesktopd default 2>/dev/null || true
    echo "Run 'rc-service webdesktopd start' to start the service."
fi
