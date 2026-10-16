FROM golang:1.27.2-bookworm AS go
FROM ubuntu@sha256:534baea6a22c03a63003dbc8dbe78fe34bc0d7e595d9a9dc9834884ff530eb55
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential pkg-config libgtk-4-dev openssh-client ca-certificates \
    python3 python3-gi gir1.2-atspi-2.0 gir1.2-gtk-3.0 at-spi2-core \
    xvfb xauth xfwm4 xdotool xclip dbus-x11 procps fonts-noto-cjk \
    && rm -rf /var/lib/apt/lists/*
COPY --from=go /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:$PATH LANG=C.UTF-8
ARG CLIENT_UID=1000
ARG CLIENT_GID=1000
# OpenSSH needs a passwd entry; permission-failure tests must run without root.
RUN if ! getent group "$CLIENT_GID" >/dev/null; then groupadd --gid "$CLIENT_GID" keyparcel-test; fi && \
    if ! getent passwd "$CLIENT_UID" >/dev/null; then useradd --uid "$CLIENT_UID" --gid "$CLIENT_GID" --no-create-home keyparcel-test; fi
# Keep the SVG runtime addition separate to reuse the cached toolchain layers.
RUN apt-get update && apt-get install -y --no-install-recommends librsvg2-common \
    && rm -rf /var/lib/apt/lists/* \
    && python3 -c "import gi; gi.require_version('GdkPixbuf', '2.0'); from gi.repository import GdkPixbuf; assert any('image/svg+xml' in f.get_mime_types() for f in GdkPixbuf.Pixbuf.get_formats()), 'GTK image requires an SVG loader'"
CMD ["sleep", "infinity"]
