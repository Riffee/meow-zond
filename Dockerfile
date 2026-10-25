FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/meow-zond ./cmd/meow-zond

FROM alpine:3.22
RUN apk add --no-cache ca-certificates iproute2 iputils
COPY --from=build /out/meow-zond /usr/local/bin/meow-zond
RUN mkdir -p /data
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/meow-zond", "-headless", "-config", "/data/probe.json"]
