FROM golang:1.20 AS builder

WORKDIR /app

WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o latency-measurer

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/latency-measurer .
CMD ["./latency-measurer"]
