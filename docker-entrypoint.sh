#!/bin/sh
set -e
# A freshly mounted volume is usually owned by root; make it writable for the unprivileged app user.
chown -R gradly:gradly "${GRADLY_DATA_DIR:-/data}" 2>/dev/null || true
exec su-exec gradly /usr/local/bin/gradly "$@"
