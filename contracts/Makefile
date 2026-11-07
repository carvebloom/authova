.PHONY: lint generate test check check-generated

lint:
	npx --yes @redocly/cli@2.54.3 lint openapi/v1/management.yaml openapi/v1/query.yaml openapi/drafts/*.yaml

generate:
	python3 ../scripts/generate-api.py

test:
	go vet ./...
	go test -race -count=1 ./...

check-generated:
	python3 scripts/check-generated.py

check: check-generated lint test
	test -z "$$(gofmt -l sdk generated mcpquery cmd examples)"
