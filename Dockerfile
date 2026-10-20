FROM --platform=linux/amd64 golang:1.26-bookworm AS build

WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /usr/local/bin/db-backup ./cmd/db-backup

FROM --platform=linux/amd64 debian:bookworm-slim

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        gzip \
        gnupg \
        wget \
        unzip; \
    rm -rf /var/lib/apt/lists/*

# Same key as MySQL 8.0.36+ (BCA43417C3B485DD128EC6D4B7B3B788A8D3785C).
# RPM-GPG-KEY-mysql-2023 is that key with an expiry of 2025-10-22; the 2025
# file is the renewed signature apt will accept.
RUN set -eux; \
    mkdir -p /etc/apt/keyrings; \
    wget -qO /tmp/mysql.asc https://repo.mysql.com/RPM-GPG-KEY-mysql-2025; \
    gpg --batch --yes --dearmor -o /etc/apt/keyrings/mysql.gpg /tmp/mysql.asc; \
    gpg --batch --show-keys --with-colons /etc/apt/keyrings/mysql.gpg \
        | awk -F: '$1 == "fpr" && $10 == "BCA43417C3B485DD128EC6D4B7B3B788A8D3785C" { found = 1 } END { exit !found }'; \
    echo 'deb [signed-by=/etc/apt/keyrings/mysql.gpg] http://repo.mysql.com/apt/debian bookworm mysql-8.4-lts' > /etc/apt/sources.list.d/mysql.list; \
    rm -f /tmp/mysql.asc

RUN set -eux; \
    apt-get update; \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends mysql-community-client; \
    rm -rf /var/lib/apt/lists/*

RUN set -eux; \
    wget -qO /tmp/rclone.zip https://github.com/rclone/rclone/releases/download/v1.75.2/rclone-v1.75.2-linux-amd64.zip; \
    unzip -j /tmp/rclone.zip rclone-v1.75.2-linux-amd64/rclone -d /usr/local/bin; \
    chmod 755 /usr/local/bin/rclone; \
    rm -f /tmp/rclone.zip

COPY --from=build /usr/local/bin/db-backup /usr/local/bin/db-backup

ENTRYPOINT ["db-backup"]
