FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# Pré-compila as dependências nesta camada: mexer no código rebuilda em segundos.
RUN CGO_ENABLED=0 go build github.com/jackc/pgx/v5/pgxpool github.com/minio/minio-go/v7 \
    github.com/prometheus/client_golang/prometheus/promauto github.com/prometheus/client_golang/prometheus/promhttp
COPY . .
RUN CGO_ENABLED=0 go build -o /vod .

# O worker chama o ffmpeg; a API usa o mesmo binário para gerar o vídeo de exemplo.
FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg fonts-dejavu-core \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /vod /vod
EXPOSE 8080 8081
ENTRYPOINT ["/vod"]
