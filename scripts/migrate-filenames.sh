#!/usr/bin/env bash
#
# Migrate existing stored PDFs to the DOI-based naming scheme used for new
# uploads: a paper with a DOI is named after a filesystem-safe form of that DOI
# (any character outside [A-Za-z0-9._-] becomes "_"), and a paper without a DOI
# keeps the "<id>.pdf" fallback. Both the file on disk and the papers.filename
# column are updated.
#
# This mirrors pdfFilename() in modules/repository/database.go. It is a one-shot
# script — new uploads already use this scheme.
#
# Usage:
#   ./scripts/migrate-filenames.sh            # uses ./archive.db and data/papers
#   DB=./archive.db PAPERS_DIR=./data/papers ./scripts/migrate-filenames.sh
#   DRY_RUN=1 ./scripts/migrate-filenames.sh  # show actions without changing anything
#
# Requires the sqlite3 CLI.

set -euo pipefail

DB="${DB:-archive.db}"
PAPERS_DIR="${PAPERS_DIR:-data/papers}"
DRY_RUN="${DRY_RUN:-0}"

if ! command -v sqlite3 >/dev/null 2>&1; then
	echo "error: sqlite3 CLI not found" >&2
	exit 1
fi
if [ ! -f "$DB" ]; then
	echo "error: database not found: $DB" >&2
	exit 1
fi

# safe_name turns a DOI into a filesystem-safe base name (no extension).
safe_name() {
	printf '%s' "$1" | sed 's/[^A-Za-z0-9._-]/_/g'
}

renamed=0
skipped=0

# Read id, doi, filename tab-separated. -noheader avoids a header row.
while IFS=$'\t' read -r id doi filename; do
	# Trim surrounding whitespace from the DOI.
	doi="$(printf '%s' "$doi" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')"

	if [ -z "$doi" ]; then
		target="${id}.pdf"
	else
		target="$(safe_name "$doi").pdf"
	fi

	# Nothing to do if the stored name already matches the target.
	if [ "$filename" = "$target" ]; then
		continue
	fi

	src="$PAPERS_DIR/$filename"
	dst="$PAPERS_DIR/$target"

	if [ -z "$filename" ] || [ ! -f "$src" ]; then
		echo "skip  #$id: source file missing (${filename:-<none>})"
		skipped=$((skipped + 1))
		continue
	fi
	if [ -e "$dst" ]; then
		echo "skip  #$id: target already exists ($target)"
		skipped=$((skipped + 1))
		continue
	fi

	echo "rename #$id: $filename -> $target"
	if [ "$DRY_RUN" != "1" ]; then
		mv "$src" "$dst"
		# Update the DB; escape single quotes for the SQL literal.
		esc_target="$(printf '%s' "$target" | sed "s/'/''/g")"
		sqlite3 "$DB" "UPDATE papers SET filename = '$esc_target' WHERE id = $id;"
	fi
	renamed=$((renamed + 1))
done < <(sqlite3 -noheader -separator $'\t' "$DB" "SELECT id, COALESCE(doi, ''), COALESCE(filename, '') FROM papers;")

note=""
if [ "$DRY_RUN" = "1" ]; then
	note=" (dry run, nothing changed)"
fi
echo "done: $renamed renamed, $skipped skipped$note"
