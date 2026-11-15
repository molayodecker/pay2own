FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/api ./cmd/api
RUN GOTOOLCHAIN=auto GOBIN=/out go install github.com/pressly/goose/v3/cmd/goose@v3.30.1

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/api /api
COPY --from=build /out/goose /goose
COPY migrations /migrations
COPY docker-entrypoint.sh /docker-entrypoint.sh
COPY deploy/pay2own.apk /pay2own.apk
RUN chmod +x /docker-entrypoint.sh
EXPOSE 8080
ENTRYPOINT ["/docker-entrypoint.sh"]
