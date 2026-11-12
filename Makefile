.PHONY: build test check integration dev-env infrastructure migrate bootstrap demo verify quality-local contract-combination
build:
	go build -trimpath -o bin/authova ./cmd/authova
test:
	go test -race -count=1 ./...
check:
	test -z "$$(gofmt -l cmd internal)"
	go vet ./...
	go test -race -count=1 ./...
dev-env:
	./scripts/local-test.sh init
infrastructure:
	./scripts/local-test.sh up
migrate: build
	bin/authova migrate
bootstrap: build
	bin/authova bootstrap
integration:
	python3 scripts/tests/go-errors.py
demo:
	./scripts/demo.sh
verify:
	./scripts/verify.sh

# Creates an owned disposable MySQL fixture; PostgreSQL is an explicit compatibility option.
quality-local:
	python3 scripts/tests/go-errors.py
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
	python3 scripts/tests/go-errors.py --focused

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

# Layered aliases supplement the legacy test/check/verify contracts. See TESTING.md.
TEST_DATABASE_DIALECT ?= mysql
.PHONY: test-discover test-unit test-component test-python test-web test-integration test-race test-e2e generate-check
test-discover:
	python3 -B scripts/test_profiles.py unit --list
	python3 -B scripts/test_profiles.py component --list
	python3 -B scripts/test_profiles.py database --list
	python3 -B scripts/test_profiles.py contract --list
test-unit:
	python3 -B scripts/test_profiles.py unit
	python3 -B scripts/test_profiles.py python-unit
test-component:
	python3 -B scripts/test_profiles.py component
	$(MAKE) test-python test-web
test-python:
	python3 -B scripts/test_profiles.py python
test-web:
	npm --prefix console/web test -- --maxWorkers=2
# Requires an already provisioned, isolated fixture. Does not create or migrate it.
test-integration:
	python3 -B scripts/test_profiles.py database --dialect $(TEST_DATABASE_DIALECT) --race
test-race: test
# Legacy browser specs require an explicitly reviewed disposable .dev fixture.
test-e2e:
	test "$(AUTHOVA_E2E_OWNED_FIXTURE)" = 1
	npm run test:e2e
generate-check: api-check ent-check
	python3 console/scripts/generate-contracts.py --check
