.PHONY: build test check integration dev-env infrastructure migrate bootstrap demo verify quality-local contract-combination
build:
	go build -trimpath -o bin/authova ./cmd/authova
test:
	go test -race -count=1 ./...
check:
	test -z "$$(gofmt -l cmd internal)"
	go vet ./...
	go test -race -count=1 ./...
dev-env: build-all
	python3 scripts/migration/instance.py init
infrastructure:
	docker-compose -p authova-migration -f deploy/migration/compose.yaml --profile mysql up -d --wait
migrate: build
	bin/authova migrate
bootstrap: build
	bin/authova bootstrap
integration:
	python3 scripts/migration/quality.py --evidence .migration/integration
demo:
	./scripts/demo.sh
verify:
	./scripts/verify.sh

# Creates disposable PG/MySQL databases and a fresh shared test root, then cleans up.
quality-local:
	python3 scripts/migration/quality.py --evidence .migration/quality-local
contract-combination:
	python3 scripts/verify-contract-combination.py

.PHONY: build-all ent migration-hash migration-test
build-all:
	go build -trimpath -o bin/authova ./cmd/authova
	go build -trimpath -o bin/authova-console ./console/cmd/authova-console
	go build -trimpath -o bin/authova-query ./contracts/cmd/authova-query
ent:
	python3 scripts/generate-ent.py
migration-hash:
	go run ./cmd/migration-hash
migration-test:
	python3 scripts/migration/quality.py --evidence .migration/acceptance

.PHONY: boundaries
boundaries:
	python3 scripts/migration/check-boundaries.py

.PHONY: api api-check api-tools
api:
	python3 scripts/generate-api.py
api-check:
	python3 scripts/generate-api.py --check
api-tools:
	python3 scripts/generate-api.py --install-tools

.PHONY: ent-check
ent-check:
	python3 scripts/generate-ent.py --check


.PHONY: schema-plan schema-plan-test
schema-plan:
	go build -trimpath -o bin/schema-plan ./cmd/schema-plan
schema-plan-test:
	python3 scripts/migration/quality.py --evidence .migration/schema-plan-gates --test-run '^Test(Atlas|EntPhysicalMapping|VersionCatalog|Maintenance|Migration)'

.PHONY: migration-upgrade-test
migration-upgrade-test: build
	python3 scripts/migration/upgrade-gates.py --evidence .migration/v1-upgrade-gates
