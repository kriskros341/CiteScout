.PHONY: docker compile test migrate-filenames hash-password

docker:
	docker build -t cite-scout .
	docker tag cite-scout:latest cite-scout:staging

compile:
	go build -v -o dist/cite-scout main.go

test:
	go test ./...

# Rename existing stored PDFs to the DOI-based scheme (one-shot). Override the
# database or storage dir with DB=... PAPERS_DIR=..., or preview with DRY_RUN=1.
migrate-filenames:
	./scripts/migrate-filenames.sh

# Print the SHA-256 hash of a password for AUTH_PASSWORD_HASH, e.g.
#   make hash-password PASSWORD=secret
hash-password:
	@printf '%s' '$(PASSWORD)' | sha256sum | cut -d' ' -f1
