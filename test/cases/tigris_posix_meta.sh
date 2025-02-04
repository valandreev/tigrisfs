#!/bin/bash

BASE_DIR=/tmp/tmp.olAMFPhcRe
#BASE_DIR=$(mktemp -d)
CASE=posix_meta
DIR=$BASE_DIR/$CASE

mkdir -p $DIR
touch $DIR/file1
ls -la $DIR
chown $USER:users $DIR/file1
ls -la $DIR
