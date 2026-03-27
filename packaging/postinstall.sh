#!/bin/sh
set -e

# Create a dedicated system user if it doesn't already exist.
if ! id -u webdesktopd > /dev/null 2>&1; then
    if command -v useradd > /dev/null 2>&1; then
        useradd --system --no-create-home --shell /sbin/nologin \
            --comment "webdesktopd daemon" webdesktopd
    elif command -v adduser > /dev/null 2>&1; then
        # Alpine
        adduser -S -H -s /sbin/nologin -G webdesktopd \
            -g "webdesktopd daemon" webdesktopd 2>/dev/null || true
        addgroup -S webdesktopd 2>/dev/null || true
    fi
fi

# Fix config dir ownership.
chown root:webdesktopd /etc/webdesktopd
chmod 750 /etc/webdesktopd
[ -f /etc/webdesktopd/env ] && chmod 640 /etc/webdesktopd/env && \
    chown root:webdesktopd /etc/webdesktopd/env

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
