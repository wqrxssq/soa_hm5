FROM golang:1.27-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY services/producer/go.mod services/producer/go.sum services/producer/tools.go ./
RUN go mod download
COPY services/producer/ ./
RUN go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen --config api/openapi/codegen.yaml -o internal/api/openapi.gen.go api/openapi/openapi.yaml
RUN CGO_ENABLED=0 go test ./...
RUN CGO_ENABLED=0 go build -o /out/producer ./cmd/producer \
    && CGO_ENABLED=0 go build -o /out/init ./cmd/init

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=build /out/producer /app/producer
COPY --from=build /out/init /app/init
USER nobody
EXPOSE 8080
ENTRYPOINT ["/app/producer"]
