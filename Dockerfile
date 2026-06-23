FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/clipshot-server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && \
    addgroup -S clipshot && adduser -S clipshot -G clipshot
COPY --from=build /out/clipshot-server /usr/local/bin/clipshot-server
RUN mkdir -p /data && chown clipshot:clipshot /data
USER clipshot
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/clipshot-server"]
