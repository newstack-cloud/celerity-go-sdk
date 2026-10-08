#!/usr/bin/env bash
# The modules in this repository, in dependency order. Sourced by the other
# scripts so that adding a module is a one-line change rather than a sweep.

MODULES=(
  "."
  "config/aws"
  "config/local"
  "serverless/aws"
  "resources/redis"
  "resources/local"
  "resources/aws"
  "telemetry/otel"
  "resources/redis/otel"
  "resources/sqldb/otel"
  "cmd/celerity-go"
)
