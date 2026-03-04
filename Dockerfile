FROM golang:1.26 AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /tmp/agentboard-api ./cmd/agentboard-api

FROM gcr.io/distroless/base-debian12

COPY --from=builder /tmp/agentboard-api /usr/local/bin/agentboard-api

ENV PORT=8080
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/agentboard-api"]
