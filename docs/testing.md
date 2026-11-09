# Testing

Gitea has four kinds of automated tests: backend unit tests, integration tests,
end-to-end (e2e) tests, and migration tests. Integration, end-to-end, and
migration tests use PostgreSQL.

For prerequisites see [build-setup.md](build-setup.md); for the build workflow see
[development.md](development.md).

## Unit tests

Backend unit tests live in `*_test.go` files next to the code they cover. Set
`GITEA_TEST_LOG_SQL=1` to log all SQL statements executed during the tests.

```bash
make test-backend
```

To run a single backend test, use `go test` directly or the `#` selector:

```bash
go test -run '^TestName$' ./modulepath/
make test-backend#TestName
```

Frontend unit tests run with [Vitest](https://vitest.dev/) browser mode:

```bash
make test-frontend
# single file:
pnpm exec vitest <path-filter>
```

## Integration tests

Integration tests exercise Gitea against PostgreSQL. They live in
`tests/integration/` and require [Git LFS](https://git-lfs.com/) to be installed.
Start PostgreSQL and MinIO, then run the integration suite:

```bash
docker run -e "POSTGRES_DB=test" -e "POSTGRES_USER=postgres" -e "POSTGRES_PASSWORD=postgres" -p 5432:5432 --rm --name pgsql postgres:latest
docker run --rm -p 9000:9000 -e MINIO_ROOT_USER=123456 -e MINIO_ROOT_PASSWORD=12345678 --name minio bitnamilegacy/minio:2023.8.31
GITEA_TEST_DATABASE=pgsql TEST_MINIO_ENDPOINT=localhost:9000 TEST_PGSQL_HOST=localhost:5432 TEST_PGSQL_DBNAME=postgres TEST_PGSQL_USERNAME=postgres TEST_PGSQL_PASSWORD=postgres make test-integration
```

Run a single integration test with the `#` selector:

```bash
make test-integration#TestName
```

Each run gets its own work directory under `tests/integration/` and random ports.
The work directory is removed after a passing run and kept after a failure.

If you hit errors such as a mismatched database version or SSH push failures, try a
clean rebuild first:

```bash
make clean build
```

### Running the database test workflow with Gitea Runner

The CI database test jobs can be run locally with
[Gitea Runner](https://gitea.com/gitea/runner). Running every job is
resource-intensive and not recommended:

```bash
gitea-runner exec -W ./.github/workflows/pull-db-tests.yml --event=pull_request --default-actions-url="https://github.com" -i catthehacker/ubuntu:runner-latest
```

List the available job names, then run a single one:

```bash
gitea-runner exec -W ./.github/workflows/pull-db-tests.yml --event=pull_request --default-actions-url="https://github.com" -i catthehacker/ubuntu:runner-latest -l
gitea-runner exec -W ./.github/workflows/pull-db-tests.yml --event=pull_request --default-actions-url="https://github.com" -i catthehacker/ubuntu:runner-latest -j <job_name>
```

## Recorded API responses

Repository migration unit and integration tests replay API responses recorded under
`_mock_data/`, so they run offline. Setting the environment variables a test reads, usually
API credentials, makes it record from the live API instead. To re-record, delete the test's
`_mock_data` directory, run the test with those variables set, and update its assertions.

## End-to-end tests

End-to-end tests drive a running Gitea instance with [Playwright](https://playwright.dev/):

The test instance uses PostgreSQL; set the `TEST_PGSQL_*` connection variables
when running it outside CI.

```bash
make test-e2e
```

To run a single e2e test file, pass it via `GITEA_TEST_E2E_FLAGS`:

```bash
GITEA_TEST_E2E_FLAGS='<filepath>' make test-e2e
```

Useful environment variables:

| Variable | Description |
| :--- | :--- |
| `GITEA_TEST_E2E_DEBUG` | When set, show the Gitea server output. |
| `GITEA_TEST_E2E_FLAGS` | Additional flags passed to Playwright, e.g. `--ui`. |
| `GITEA_TEST_E2E_TIMEOUT_FACTOR` | Timeout multiplier (default: 4 on CI, 1 locally). |

## Migration tests

If you change a database-persisted struct under `models/` you will usually need a
new migration in `modelmigration`. Run the migration tests with:

```bash
make test-migration
```

## Continuous integration

CI runs the unit tests, runs integration and migration tests against PostgreSQL,
and tests migration from several recent Gitea versions. Please submit your pull
request with additional unit and integration tests as appropriate. Prefer unit tests
when the logic can be tested in isolation, and keep local integration and e2e tests
fast (aim for sub-2s runtime).
