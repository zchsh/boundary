#!/usr/bin/env bash

# This script sets up a test environment against which all tests can be run (go test ./...)

export $(go run scripts/setupdockerdb.go)
go test -count=1 -p 10 ./internal/...