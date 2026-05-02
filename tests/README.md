This directory contains integration tests that exercise tea against external services or external executables.

- Unit tests stay next to the packages they cover.
- Integration tests live under `tests/` so they can be run separately.

Common targets:

- `make unit-test`
- `make integration-test`
- `make test`
