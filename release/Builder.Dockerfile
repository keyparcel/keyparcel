ARG BASE=ubuntu:24.04
FROM golang:1.27.2-bookworm AS go
FROM ${BASE}
RUN if command -v apt-get; then apt-get update && apt-get install -y --no-install-recommends build-essential pkg-config libgtk-4-dev dpkg-dev ca-certificates python3; else dnf --setopt=install_weak_deps=False install -y gcc pkgconf-pkg-config gtk4-devel rpm-build ca-certificates python3; fi
COPY --from=go /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:$PATH
WORKDIR /src
