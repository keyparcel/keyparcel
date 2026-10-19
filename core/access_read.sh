set -eu
LC_ALL=C; export LC_ALL
if [ "$2" = discovery ]; then
    [ -r "$1" ] || { printf 'KP-IO\n'; exit 1; }
    printf 'KP-DISCOVERY-1\n'
    head -c 4194305 "$1"
else
    metadata=$(stat -c '%f %s' "$1") || { printf 'KP-IO\n'; exit 1; }
    set -- "$1" $metadata
    [ "$#" = 3 ] || exit 1
    case "$2" in 8???) ;; *) printf 'KP-NONREGULAR\n'; exit 1;; esac
    [ -r "$1" ] || { printf 'KP-IO\n'; exit 1; }
    case "$3" in *[!0-9]*|"") exit 1;; esac
    printf 'KP-PARCEL-1 %s\n' "$3"
    head -c "$3" "$1"
fi
