#!/bin/bash

# This script creates an JSON Schema repo from github.com/modelcontextprotocol/go-sdk (and friends).
# It will be used as a one-off to create github.com/google/jsonschema-go/.
#
# Requires https://github.com/newren/git-filter-repo.

set -eu

# Check if exactly one argument is provided
if [ "$#" -ne 1 ]; then
  echo "create-repo.sh: create a standalone JSON Schema repo from modelcontextprotocol/go-sdk"
  echo "Usage: $0 <JSON Schema repo destination>"
  exit 1
fi >&2

src=$(go list -m -f {{.Dir}} github.com/modelcontextprotocol/go-sdk)
dest="$1"

echo "Filtering JSON Schema commits from ${src} to ${dest}..." >&2

startdir=$(pwd)
tempdir=$(mktemp -d)
function cleanup {
  echo "cleaning up ${tempdir}"
  rm -rf "$tempdir"
} >&2
trap cleanup EXIT SIGINT

echo "Checking out to ${tempdir}"

git clone --bare "${src}" "${tempdir}"
git -C "${tempdir}" --git-dir=. filter-repo \
  --subdirectory-filter jsonschema/ \
  --replace-text "${startdir}/mcp-repo-replace.txt" \
  --force
mkdir ${dest}
cd "${dest}"
git init
git remote add filtered_source "${tempdir}"
git pull filtered_source main --allow-unrelated-histories -X ours
git remote remove filtered_source
go mod init github.com/google/jsonschema-go && go get go@1.24.5
go mod tidy
git add go.mod go.sum
git commit -m "all: add go.mod and go.sum file"
