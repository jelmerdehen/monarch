#!/bin/sh
# mon_off - Stop all monarch capture/compress units and legacy mon services.

set -e

# 1. Stop active monarch screencast session if one is currently recording
if command -v monarch >/dev/null 2>&1; then
	monarch screencast stop 2>/dev/null || true
fi

# 2. Stop all monarch systemd user services and timers
if command -v systemctl >/dev/null 2>&1; then
	systemctl --user stop 'monarch_*' 2>/dev/null || true

	# 3. Stop legacy system-level mon units if any are active
	for unit in mon_srec.service mon_air.service mon_dbus_system.service mon_udev.service; do
		if systemctl is-active --quiet "$unit" 2>/dev/null; then
			printf 'Stopping %s\n' "$unit"
			systemctl stop "$unit" 2>/dev/null || true
		fi
	done
fi
