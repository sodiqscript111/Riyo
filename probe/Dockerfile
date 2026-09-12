FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o ha-probe main.go

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/ha-probe .

EXPOSE 8000
CMD ["./ha-probe"]
