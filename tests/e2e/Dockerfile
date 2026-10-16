FROM alpine@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
RUN apk add --no-cache openssh && adduser -D parcel && echo 'parcel:test-only-password' | chpasswd && mkdir -p /home/parcel/.ssh && chown parcel:parcel /home/parcel/.ssh && chmod 700 /home/parcel/.ssh
COPY sshd_config /etc/ssh/sshd_config
CMD ["sh", "-c", "syslogd -O /var/log/sftp.log && ssh-keygen -q -t ed25519 -N '' -f /etc/ssh/ssh_host_ed25519_key && exec /usr/sbin/sshd -D -e"]
