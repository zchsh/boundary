#!/usr/bin/env bash

# This script sets up a test environment against which all tests can be run (go test ./...)
tmpfile=$(mktemp /tmp/boundarytest.XXXXX)
export BOUNDARY_DB_TEST=$(go run scripts/setupdockerdb.go --docker_info_file=${tmpfile})

go test -count=1 -p 15 ./...

CONTAINER_NAME=$(cat ${tmpfile})
docker rm -f -v ${CONTAINER_NAME}