#!/usr/bin/env sh
set -eu

version_file="${VERSION_FILE:-VERSION}"
constants_file="${CONSTANTS_FILE:-common/constants.go}"
expected_version="${1:-}"

if [ ! -f "$version_file" ]; then
  echo "Missing version file: $version_file" >&2
  exit 1
fi

if [ ! -f "$constants_file" ]; then
  echo "Missing constants file: $constants_file" >&2
  exit 1
fi

file_version="$(tr -d '[:space:]' < "$version_file")"
constant_version="$(sed -n 's/^var Version = "\([^"]*\)".*/\1/p' "$constants_file" | head -n 1)"

if [ -z "$file_version" ]; then
  echo "VERSION is empty" >&2
  exit 1
fi

if [ -z "$constant_version" ]; then
  echo "Could not read common.Version from $constants_file" >&2
  exit 1
fi

if [ "$file_version" != "$constant_version" ]; then
  echo "Version mismatch: $version_file=$file_version, common.Version=$constant_version" >&2
  exit 1
fi

if [ -n "$expected_version" ] && [ "$expected_version" != "$file_version" ]; then
  echo "Version mismatch: expected=$expected_version, $version_file=$file_version" >&2
  exit 1
fi

echo "Version sync OK: $file_version"
