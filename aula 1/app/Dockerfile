FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# Pré-compila as dependências nesta camada: mexer no main.go rebuilda em segundos.
RUN CGO_ENABLED=0 go build github.com/jackc/pgx/v5/pgxpool github.com/redis/go-redis/v9 \
    github.com/prometheus/client_golang/prometheus/promauto github.com/prometheus/client_golang/prometheus/promhttp
COPY . .
RUN CGO_ENABLED=0 go build -o /shortener .

FROM alpine:3.20
COPY --from=build /shortener /shortener
EXPOSE 8080
ENTRYPOINT ["/shortener"]
