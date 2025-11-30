FROM golang:1.25 AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /bin/subscription ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates

COPY --from=builder /bin/subscription /usr/local/bin/subscription

WORKDIR /app
COPY docs docs

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/subscription"]
