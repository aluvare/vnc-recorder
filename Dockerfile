FROM golang:1.21-bookworm AS build-env

WORKDIR /app

# Cache dependencies separately from source code.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /vnc-recorder

FROM linuxserver/ffmpeg:version-6.1.1-cli
COPY --from=build-env /vnc-recorder /
ENTRYPOINT ["/vnc-recorder"]
