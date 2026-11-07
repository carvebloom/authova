.PHONY: build check generate check-generated
build:
	npm --prefix web ci
	npm --prefix web run build
	go build -trimpath -o bin/authova-console ./cmd/authova-console
check: check-generated
	go vet ./...
	go test -race -count=1 ./...
	npm --prefix web run typecheck
	npm --prefix web run lint
	npm --prefix web run format:check
	npm --prefix web test
	npm --prefix web run build
generate:
	python3 scripts/generate-contracts.py
check-generated:
	python3 scripts/generate-contracts.py --check
