# The image `go run ./release <version>` builds for linux/amd64 and
# linux/arm64. Its context is release/out/image, which holds only the two
# image executables, so no file of the working tree can reach a layer. No
# step runs inside the image, so neither platform needs emulation.

# Only the CA bundle is taken from this image (S7); one platform's copy
# serves both.
FROM --platform=$BUILDPLATFORM alpine:3.22.2 AS certificates

FROM scratch
ARG TARGETARCH
COPY --from=certificates /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --chmod=0755 linux-${TARGETARCH}/bazarrbulksync /bazarrbulksync
# Exec form: the tool is process 1 and receives a container stop's SIGTERM
# itself (S8); a shell form would leave the signal with a shell.
ENTRYPOINT ["/bazarrbulksync"]
