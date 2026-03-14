FROM ubuntu:24.04

ARG GO_VERSION=1.26.1
ARG TARGETARCH

ENV DEBIAN_FRONTEND=noninteractive
ENV PATH=/usr/local/go/bin:/root/go/bin:${PATH}
ENV PIP_BREAK_SYSTEM_PACKAGES=1

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      acl \
      build-essential \
      ca-certificates \
      curl \
      gawk \
      git \
      libacl1-dev \
      libexpect-perl \
      libselinux1-dev \
      locales-all \
      make \
      perl \
      pkg-config \
      python3 \
      python3-pip \
      valgrind \
      xz-utils && \
    python3 -m pip install --disable-pip-version-check pyinotify && \
    case "${TARGETARCH}" in \
      amd64|arm64) GO_ARCH="${TARGETARCH}" ;; \
      "") GO_ARCH=amd64 ;; \
      *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz" | tar -C /usr/local -xz && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

ARG GNU_COREUTILS_VERSION=9.10
ARG GNU_COREUTILS_SHA256=e0bde1fb68509447fc723cf2517e8a8c7fa46769919bb7490ed350a2e9238562

RUN curl -fsSL "https://ftp.gnu.org/gnu/coreutils/coreutils-${GNU_COREUTILS_VERSION}.tar.gz" \
      -o /tmp/coreutils.tar.gz && \
    echo "${GNU_COREUTILS_SHA256}  /tmp/coreutils.tar.gz" | sha256sum -c - && \
    mkdir -p /opt/gnu && \
    tar -xzf /tmp/coreutils.tar.gz -C /opt/gnu && \
    cd "/opt/gnu/coreutils-${GNU_COREUTILS_VERSION}" && \
    FORCE_UNSAFE_CONFIGURE=1 ./configure --disable-nls --disable-dependency-tracking && \
    make -j"$(nproc)" && \
    rm /tmp/coreutils.tar.gz

WORKDIR /workspace
