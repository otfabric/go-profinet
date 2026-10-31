# The container go-profinet's otfnode runs in during the interop tests:
# Debian with iproute2 (a DCP Set IP is applied to the interface) and
# tcpdump (capture sidecars).
FROM debian:bookworm-20251020-slim@sha256:78d2f66e0fec9e5a39fb2c72ea5e052b548df75602b5215ed01a17171529f706
RUN apt-get update && apt-get install -y --no-install-recommends iproute2 tcpdump && rm -rf /var/lib/apt/lists/*
