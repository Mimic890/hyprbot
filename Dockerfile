FROM golang:1.25.6-alpine AS builder
WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/hyprbot ./cmd/bot

FROM alpine:3.20
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /out/hyprbot /usr/local/bin/hyprbot
COPY migrations ./migrations

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/hyprbot"]
