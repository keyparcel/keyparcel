set -eu
LC_ALL=C; export LC_ALL
# Missing/inaccessible directories are classified by ordinary SFTP, not shell errors.
if [ ! -d "$1" ] || [ ! -r "$1" ] || [ ! -x "$1" ]; then
    printf 'KP-SFTP\n'; exit 0
fi
printf 'KP-INBOX-1\n'
find "$1" -mindepth 1 -maxdepth 1 -name '*.age' -exec sh -c '
set -eu
for file do
    base=${file##*/}; id=${base%.age}
    case "$id" in *[!0-9a-f]*|"") continue;; esac
    [ "${#id}" = 32 ] || continue
    metadata=$(stat -c "%f %Y %s" "$file") || { printf "X %s io\n" "$id"; continue; }
    set -- $metadata
    [ "$#" = 3 ] || exit 1
    case "$1" in 8???) ;; *) printf "X %s nonregular\n" "$id"; continue;; esac
    size=$3
    case "$size" in *[!0-9]*|"") exit 1;; esac
    [ "$size" -le 1048576 ] || size=1048576
    printf "R %s %s %s\n" "$id" "$2" "$size"
    head -c "$size" "$file" || exit 1
    printf "\n"
done
' sh {} + || exit 1
printf 'END\n'
