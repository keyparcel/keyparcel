set -eu
LC_ALL=C; export LC_ALL
for tool in sh find stat head; do command -v "$tool" >/dev/null || exit 1; done
metadata=$(stat -c '%f %Y %s' /dev/null) || exit 1
set -- $metadata
[ "$#" = 3 ] && [ "$3" = 0 ] || exit 1
case "$1" in 2???) ;; *) exit 1;; esac
# Require find to propagate a batched child failure, not hide listing errors.
if find /dev/null -mindepth 0 -maxdepth 0 -exec sh -c 'exit 7' sh {} +; then exit 1; fi
printf 'KP-CAP-1\n'
printf '\000\377x' | head -c 2
printf '\n'
