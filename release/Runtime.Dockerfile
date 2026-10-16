ARG BASE=ubuntu:24.04
FROM ${BASE}
COPY . /candidate
# Minimal images filter docs; retain candidate docs during install validation.
RUN if command -v apt-get; then printf 'path-include=/usr/share/doc/keyparcel/*\n' > /etc/dpkg/dpkg.cfg.d/zz-keyparcel-docs; fi
# Install only the candidate and its declared runtime dependencies first.
RUN if command -v apt-get; then apt-get update && apt-get install -y --no-install-recommends /candidate/*.deb; else dnf --setopt=tsflags= --setopt=install_weak_deps=False install -y /candidate/*.rpm; fi
RUN /usr/bin/keyparcel --version && /usr/bin/keyparcel-gtk --version && ! ldd /usr/bin/keyparcel-gtk | grep 'not found'
# These tools drive the existing journeys; no compiler or development binary.
RUN if command -v apt-get; then apt-get install -y --no-install-recommends libglib2.0-bin python3 python3-gi gir1.2-atspi-2.0 gir1.2-gtk-3.0 at-spi2-core xvfb xauth xfwm4 xdotool xclip dbus-x11 procps fonts-noto-cjk desktop-file-utils bash-completion; else dnf --setopt=install_weak_deps=False install -y python3 python3-gobject gobject-introspection gtk3 at-spi2-core xorg-x11-server-Xvfb xorg-x11-xauth xfwm4 xdotool xclip dbus-daemon dbus-tools procps-ng google-noto-sans-cjk-fonts desktop-file-utils bash-completion; fi
RUN python3 -c "import gi; gi.require_version('Atspi', '2.0'); gi.require_version('Gdk', '3.0'); from gi.repository import Atspi, Gdk, GLib"
ARG CLIENT_UID=1000
ARG CLIENT_GID=1000
RUN if ! getent group "$CLIENT_GID" >/dev/null; then groupadd --gid "$CLIENT_GID" keyparcel-test; fi && if ! getent passwd "$CLIENT_UID" >/dev/null; then useradd --uid "$CLIENT_UID" --gid "$CLIENT_GID" --create-home keyparcel-test; fi
CMD ["sleep", "infinity"]
