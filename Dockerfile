FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /emulator ./cmd/emulator

FROM alpine:3.19

RUN apk add --no-cache ca-certificates

COPY --from=builder /emulator /usr/local/bin/emulator

EXPOSE 8123

ENTRYPOINT ["emulator"]
