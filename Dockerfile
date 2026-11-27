# Runtime image for the HTTP transport. GoReleaser builds the binary (see
# .goreleaser.yaml, dockers_v2) and passes it in the build context under
# $TARGETPLATFORM, so this stays a copy rather than a second Go build.
FROM alpine:3.22

# ffmpeg decodes the H.264 frames behind take_screenshot and record_video.
RUN apk add --no-cache ffmpeg ca-certificates

ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/mcp-jetkvm /usr/local/bin/mcp-jetkvm

USER 65534:65534
EXPOSE 8080
# The container is only useful over HTTP, and --auth none is refused off
# loopback, so default to bearer (set JETKVM_MCP_BEARER_TOKEN).
ENTRYPOINT ["/usr/local/bin/mcp-jetkvm"]
CMD ["--http", ":8080", "--auth", "bearer"]
