FROM oven/bun:1-alpine AS client
WORKDIR /usr/local/src/client
COPY client/package.json client/bun.lock client/bunfig.toml ./
RUN bun install --frozen-lockfile
COPY client/ ./
# The client's tests read the shared paint vectors from ../protocol.
COPY protocol/ /usr/local/src/protocol/
RUN bun run build

FROM cgr.dev/chainguard/go:latest AS server
WORKDIR /usr/local/src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /usr/local/bin/wetpaint ./cmd/server && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /usr/local/bin/wetpaint-bots ./cmd/bots

FROM cgr.dev/chainguard/busybox:latest
COPY --from=server /usr/local/bin/wetpaint /usr/local/bin/wetpaint-bots /usr/local/bin/
COPY --from=client /usr/local/src/client/dist /static
EXPOSE 8080
# CMD rather than ENTRYPOINT so a Deploio worker job can run wetpaint-bots
# from the same image.
CMD ["/usr/local/bin/wetpaint", "-static", "/static"]
