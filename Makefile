.PHONY: generate fmt test up down e2e

generate:
	cd services/producer && go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen --config api/openapi/codegen.yaml -o internal/api/openapi.gen.go api/openapi/openapi.yaml
	cd services/aggregation && go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen --config api/openapi/codegen.yaml -o internal/api/openapi.gen.go api/openapi/openapi.yaml
	gofmt -w services/producer/internal/api/openapi.gen.go services/aggregation/internal/api/openapi.gen.go

fmt:
	gofmt -w services/producer services/aggregation

test: generate
	test -z "$$(gofmt -l services/producer services/aggregation)"
	cd services/producer && go test ./...
	cd services/aggregation && go test ./...

up:
	docker compose up --build

down:
	docker compose down -v

e2e:
	python3 scripts/e2e.py
