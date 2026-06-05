#!/bin/sh
# Entrypoint for the forgejo-ha image : spawn Forgejo in the background,
# then exec weft-ha-forgejo in the foreground. The trap ensures both
# processes die together — if Forgejo crashes the agent goes down (so
# the L7 pool sees an offline replica and weft-agent reschedules) ;
# conversely if the agent dies we tear Forgejo down so the
# next-boot agent re-bootstraps cleanly.
#
# We pass through every CLI flag to weft-ha-forgejo. Forgejo itself
# reads /etc/forgejo/app.ini (rendered by weft-init before the
# microVM boots ; that's why the agent doesn't render it here).

set -eu

FORGEJO_BIN="${FORGEJO_BIN:-/usr/local/bin/forgejo}"
FORGEJO_WORK_DIR="${FORGEJO_WORK_DIR:-/var/lib/forgejo}"
FORGEJO_CUSTOM="${FORGEJO_CUSTOM:-/etc/forgejo}"

export FORGEJO_WORK_DIR FORGEJO_CUSTOM

# 1. Spawn Forgejo in the background.
"${FORGEJO_BIN}" web --config "${FORGEJO_CUSTOM}/app.ini" &
forgejo_pid=$!

# 2. Trap signals + child exit. If Forgejo dies first we propagate;
#    if the agent dies first we kill Forgejo before exiting.
cleanup() {
    rc=$?
    if kill -0 "${forgejo_pid}" 2>/dev/null; then
        kill -TERM "${forgejo_pid}" 2>/dev/null || true
        # Give Forgejo a few seconds for a clean DB connection drain.
        for _ in 1 2 3 4 5; do
            if ! kill -0 "${forgejo_pid}" 2>/dev/null; then
                break
            fi
            sleep 1
        done
        kill -KILL "${forgejo_pid}" 2>/dev/null || true
    fi
    exit "${rc}"
}
trap cleanup EXIT INT TERM

# 3. Background watcher : if Forgejo exits, kill our own PID so the
#    foreground agent gets SIGTERM and the trap above tears down.
(
    wait "${forgejo_pid}"
    forgejo_rc=$?
    echo "forgejo exited rc=${forgejo_rc} — bringing the replica down" >&2
    kill -TERM $$ 2>/dev/null || true
) &

# 4. Exec the agent in the foreground.
exec /usr/local/bin/weft-ha-forgejo agent "$@"
