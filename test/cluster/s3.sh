#!/bin/bash

echo "=== Use S3 ==="

#unset ENDPOINT

env|grep ENDPOINT

TEST_ARTIFACTS="${TEST_ARTIFACTS:-executions/$(date +"%Y-%m-%d-%H-%M-%S")}"
mkdir -p "$TEST_ARTIFACTS"
export S3CMD_ARGS="--access_key=foo --secret_key=bar"

_kill_s3proxy() {
    :
}

_s3cmd() {
  s3cmd \
  $S3CMD_ARGS \
  --signature-v2 \
  --no-ssl \
  --host-bucket="" \
  --host="$ENDPOINT" \
  "$@"
}

_aws_cli() {
  aws s3 --endpoint-url="$ENDPOINT" "$@"
}

export BUCKET_NAME="test.$RANDOM"

_aws_cli mb s3://$BUCKET_NAME

