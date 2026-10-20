"""Files independently owned by each Linux client package/archive."""
CLIENTS = ('cli', 'gtk')
NOTICES = ('LICENSE', 'NOTICE', 'THIRD_PARTY_NOTICES.txt', 'copyright')


def files(client):
    name = 'keyparcel-' + client
    paths = {'usr/bin/' + ('keyparcel' if client == 'cli' else name): 0o755}
    if client == 'cli':
        paths['usr/share/bash-completion/completions/keyparcel'] = 0o644
        paths['usr/share/man/man1/keyparcel.1.gz'] = 0o644
    else:
        paths['usr/share/applications/io.github.keyparcel.keyparcel.desktop'] = 0o644
        paths['usr/share/icons/hicolor/scalable/apps/io.github.keyparcel.keyparcel.svg'] = 0o644
    paths.update({'usr/share/doc/' + name + '/' + leaf: 0o644
                  for leaf in (*NOTICES, 'README.md', 'build-info.json')})
    return paths
