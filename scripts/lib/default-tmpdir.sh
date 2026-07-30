#!/usr/bin/env sh
# Print the TMPDIR the test-running wrappers should use when the calling shell
# has not set TMPDIR itself. Every wrapper that builds its own `env -i` around
# `go test` (the Makefile's TEST_ENV and the shard scripts) resolves its default
# through here so the policy lives in one place.
#
# The policy has two constraints:
#
#   Off the shared tmpfs. On the Linux fleet /tmp is a size-capped RAM-backed
#   tmpfs shared by every executor, and filling it ENOSPCs unrelated agents (see
#   AGENTS.md "Build Cache Conventions"). /var/tmp is on disk, so it wins
#   wherever it exists -- which is every Linux and macOS host.
#
#   Short. internal/testutil.ShortTempDir roots test-owned socket directories at
#   os.TempDir(), and the Unix socket paths built under it must stay inside
#   sun_path (104 bytes on macOS, 108 on Linux; AF_UNIX on Windows uses the same
#   108-byte sockaddr_un). Both candidates are short enough to leave ample room.
#
# Git for Windows ships no /var at all, so the /var/tmp default was simply a
# nonexistent path there: mktemp failed and anything reading $TMPDIR broke. Its
# /tmp is not a tmpfs -- it maps to the on-disk user temp directory -- so
# falling back to /tmp satisfies both constraints on exactly the platform that
# lacks /var/tmp, while hosts that have /var/tmp never reach the fallback.

if [ -n "${TMPDIR:-}" ]; then
    printf '%s\n' "$TMPDIR"
    exit 0
fi

for candidate in /var/tmp /tmp; do
    if [ -d "$candidate" ] && [ -w "$candidate" ]; then
        printf '%s\n' "$candidate"
        exit 0
    fi
done

echo "default-tmpdir: no writable temp directory among \$TMPDIR, /var/tmp, /tmp" >&2
exit 1
