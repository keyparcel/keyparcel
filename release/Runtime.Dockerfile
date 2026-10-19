ARG BASE=ubuntu:24.04
FROM ${BASE}
ARG CLIENT
ENV KEYPARCEL_PACKAGE_CLIENT=${CLIENT}
COPY . /candidate
# Keep package documentation even on minimal distro images.
RUN if command -v apt-get; then printf 'path-include=/usr/share/doc/keyparcel-*/*\npath-include=/usr/share/man/man1/keyparcel.1.gz\n' > /etc/dpkg/dpkg.cfg.d/zz-keyparcel-docs; fi
# Check the independently installed product before adding any test-driver tools.
RUN if command -v apt-get; then apt-get update && apt-get install -y --no-install-recommends /candidate/keyparcel-${CLIENT}_*.deb; else dnf --setopt=tsflags= --setopt=install_weak_deps=False install -y /candidate/keyparcel-${CLIENT}-*.rpm; fi
RUN if [ "$CLIENT" = cli ]; then /usr/bin/keyparcel --version && test ! -e /usr/bin/keyparcel-gtk && test ! -d /usr/share/icons/Adwaita && ! find /usr -name 'libgtk*' -print | grep .; else /usr/bin/keyparcel-gtk --version && test ! -e /usr/bin/keyparcel && ! ldd /usr/bin/keyparcel-gtk | grep 'not found'; fi
RUN if command -v apt-get; then apt-get install -y --no-install-recommends python3 bash-completion man-db groff-base; else dnf --setopt=install_weak_deps=False install -y python3 bash-completion man-db groff-base; fi
RUN if [ "$CLIENT" = gtk ]; then \
    if command -v apt-get; then apt-get install -y --no-install-recommends libglib2.0-bin python3-gi gir1.2-atspi-2.0 gir1.2-gtk-3.0 at-spi2-core xvfb xauth xfwm4 xdotool xclip dbus-x11 procps fonts-noto-cjk desktop-file-utils; else dnf --setopt=install_weak_deps=False install -y python3-gobject gobject-introspection gtk3 at-spi2-core xorg-x11-server-Xvfb xorg-x11-xauth xfwm4 xdotool xclip dbus-daemon dbus-tools procps-ng google-noto-sans-cjk-fonts desktop-file-utils; fi; \
    python3 -c "import gi; gi.require_version('Atspi', '2.0'); gi.require_version('Gdk', '3.0'); from gi.repository import Atspi, Gdk, GLib"; fi
ARG CLIENT_UID=1000
ARG CLIENT_GID=1000
RUN if ! getent group "$CLIENT_GID" >/dev/null; then groupadd --gid "$CLIENT_GID" keyparcel-test; fi && if ! getent passwd "$CLIENT_UID" >/dev/null; then useradd --uid "$CLIENT_UID" --gid "$CLIENT_GID" --create-home keyparcel-test; fi
CMD ["sleep", "infinity"]
