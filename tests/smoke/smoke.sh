#!/usr/bin/env bash
set -euo pipefail
repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
state_dir="$repo_dir/.smoke"
read -r -a docker_cmd <<< "${KEYPARCEL_DOCKER:-docker}"
read -r -a go_cmd <<< "${KEYPARCEL_GO:-go}"
fail() { echo "$*" >&2; exit 1; }
load() {
    [[ -f "$state_dir/name" ]] || fail 'Run make smoke-up first.'
    name=$(cat "$state_dir/name")
    [[ "$name" =~ ^keyparcel-smoke-[0-9]+-[0-9]+$ ]] || fail 'Invalid smoke resource name.'
    "${docker_cmd[@]}" info --format '{{.ServerVersion}}' >/dev/null
}
client() { local actor=$1; shift; "${docker_cmd[@]}" exec -i "$name-$actor" "$@"; }
ready() {
    local actor=$1
    for ((attempt=0; attempt<100; attempt++)); do
        if client "$actor" keyparcel devices >/dev/null 2>&1; then return; fi
        sleep 0.1
    done
    "${docker_cmd[@]}" logs "$name-server" >&2
    fail "Smoke client $actor could not connect; inspect with make smoke-$actor or reset with make smoke-down."
}
refresh_clients() {
    CGO_ENABLED=0 "${go_cmd[@]}" build -o "$state_dir/keyparcel" "$repo_dir/cmd/keyparcel"
    chmod 755 "$state_dir/keyparcel"
    for actor in alice bob; do
        "${docker_cmd[@]}" cp "$state_dir/keyparcel" "$name-$actor:/usr/local/bin/.keyparcel-smoke-$$"
        "${docker_cmd[@]}" exec --user 0 "$name-$actor" mv "/usr/local/bin/.keyparcel-smoke-$$" /usr/local/bin/keyparcel
    done
    rm "$state_dir/keyparcel"
}
show_ready() {
    echo 'Ready. Open make smoke-alice and make smoke-bob in separate terminals.'
    if [[ -f "$state_dir/fresh" ]]; then
        echo 'Fresh host-trust mode: Alice approves the server on first connection; Bob already trusts it.'
        echo "Compare Alice's OpenSSH prompt with this fixture server fingerprint:"
        client bob ssh-keygen -lf - < "$state_dir/host.pub"
        echo 'In Alice, run keyparcel devices and confirm only after comparing the fingerprint.'
        echo "Ordinary smoke-up preserves approval; smoke-fresh clears only Alice's server trust."
    fi
    echo "Files persist in $state_dir/alice and $state_dir/bob until make smoke-down."
}
action=${1:-}
case "$action" in
up|fresh)
    if [[ -e "$state_dir" ]]; then
        load
        for actor in server alice bob; do
            [[ $("${docker_cmd[@]}" inspect --format '{{.State.Running}}' "$name-$actor") == true ]] || fail 'Smoke environment is incomplete or stopped; run make smoke-down, then make smoke-up.'
        done
        (cd "$repo_dir" && refresh_clients)
        if [[ "$action" == fresh ]]; then
            rm -f -- "$state_dir/alice/.ssh/known_hosts"
            touch "$state_dir/fresh"
        fi
        show_ready
        exit 0
    fi
    bash "$repo_dir/tests/build-images.sh"
    umask 077
    mkdir "$state_dir"
    name="keyparcel-smoke-$(date +%s)-$$"
    printf '%s\n' "$name" > "$state_dir/name"
    [[ "$action" != fresh ]] || touch "$state_dir/fresh"
    trap 'echo "Smoke setup failed; state retained. Run make smoke-down to reset." >&2' ERR
    "${docker_cmd[@]}" network create --internal "$name" >/dev/null
    "${docker_cmd[@]}" run -d --name "$name-server" --network "$name" --network-alias inbox keyparcel-test-sshd:local >/dev/null
    for actor in alice bob; do
        mkdir -p "$state_dir/$actor/.ssh" "$state_dir/$actor/Downloads"
        printf 'source /usr/local/share/keyparcel/completion.bash\n' > "$state_dir/$actor/.bashrc"
        client_image=keyparcel-test-client:local
        [[ "$actor" != bob ]] || client_image=keyparcel-test-clipboard:local
        # HOME matches the passwd entry so OpenSSH finds its normal configuration.
        client_home=$("${docker_cmd[@]}" run --rm "$client_image" getent passwd "$(id -u)" | cut -d: -f6)
        [[ -n "$client_home" ]] || fail 'Missing client user home.'
        "${docker_cmd[@]}" run -d --init --hostname "$actor" --name "$name-$actor" --network "$name" --user "$(id -u):$(id -g)" --env "HOME=$client_home" --workdir "$client_home" --mount "type=bind,src=$state_dir/$actor,dst=$client_home" "$client_image" >/dev/null
        client "$actor" sh -c 'ssh-keygen -q -t ed25519 -N "" -C "$1" -f "$HOME/.ssh/id_ed25519"' sh "$actor"
        cat "$state_dir/$actor/.ssh/id_ed25519.pub" >> "$state_dir/authorized_keys"
        cat > "$state_dir/$actor/.ssh/config" <<CONFIG
Host inbox
 HostName inbox
 User parcel
 IdentityFile $client_home/.ssh/id_ed25519
 IdentityAgent none
 StrictHostKeyChecking ask
 GlobalKnownHostsFile /dev/null
CONFIG
    done
    "${docker_cmd[@]}" exec -i "$name-server" sh -c 'cat > /home/parcel/.ssh/authorized_keys; chown parcel:parcel /home/parcel/.ssh/authorized_keys; chmod 600 /home/parcel/.ssh/authorized_keys' < "$state_dir/authorized_keys"
    for ((attempt=0; attempt<100; attempt++)); do
        if "${docker_cmd[@]}" exec "$name-server" cat /etc/ssh/ssh_host_ed25519_key.pub > "$state_dir/host.pub" 2>/dev/null; then break; fi
        sleep 0.1
    done
    [[ -s "$state_dir/host.pub" ]] || fail 'SSH server did not generate its host key.'
    for actor in alice bob; do
        read -r key_type key_data comment < "$state_dir/host.pub"
        if [[ "$action" != fresh || "$actor" != alice ]]; then
            printf 'inbox %s %s\n' "$key_type" "$key_data" > "$state_dir/$actor/.ssh/known_hosts"
        fi
        client "$actor" keyparcel setup --host inbox --default
    done
    ready bob
    [[ "$action" == fresh ]] || ready alice
    show_ready
    ;;
alice|bob)
    load
    "${docker_cmd[@]}" exec -it "$name-$1" bash
    ;;
down)
    [[ -e "$state_dir" ]] || { echo 'No smoke environment to remove.'; exit 0; }
    load
    for actor in alice bob server; do
        if "${docker_cmd[@]}" container inspect "$name-$actor" >/dev/null 2>&1; then
            "${docker_cmd[@]}" rm -f "$name-$actor" >/dev/null
        fi
    done
    if "${docker_cmd[@]}" network inspect "$name" >/dev/null 2>&1; then
        "${docker_cmd[@]}" network rm "$name" >/dev/null
    fi
    rm -rf -- "$state_dir"
    echo 'Removed smoke containers, network, and fixture data.'
    ;;
*) fail 'Usage: smoke.sh up|fresh|alice|bob|down' ;;
esac
