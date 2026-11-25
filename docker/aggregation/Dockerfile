FROM golang:1.27-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY services/aggregation/go.mod services/aggregation/go.sum services/aggregation/tools.go ./
RUN go mod download
COPY services/aggregation/ ./
RUN go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen --config api/openapi/codegen.yaml -o internal/api/openapi.gen.go api/openapi/openapi.yaml
RUN CGO_ENABLED=0 go test ./...
RUN CGO_ENABLED=0 go build -o /out/aggregation ./cmd/aggregation \
    && CGO_ENABLED=0 go build -o /out/bucket ./cmd/bucket

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=build /out/aggregation /app/aggregation
COPY --from=build /out/bucket /app/bucket
USER nobody
EXPOSE 8080
ENTRYPOINT ["/app/aggregation"]
