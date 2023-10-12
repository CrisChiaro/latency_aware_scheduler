FROM golang:1.20 AS builder

WORKDIR /app

WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o latency-meter

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/latency-meter .
CMD ["./latency-meter"]