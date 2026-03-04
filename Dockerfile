FROM golang:1.26 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o agentboard ./cmd/agentboard

FROM gcr.io/distroless/base-debian12
WORKDIR /app

COPY --from=builder /app/agentboard /usr/local/bin/agentboard

ENV PORT=8080
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/agentboard","api","--bind","0.0.0.0"]
