FROM golang:1.24-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/tds-client-proxy ./cmd/client_proxy

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /out/tds-client-proxy /app/tds-client-proxy
EXPOSE 5100
ENTRYPOINT ["/app/tds-client-proxy"]
