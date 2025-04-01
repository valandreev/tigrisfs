#!/bin/bash
# Copyright 2025 Tigris Data, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

BASE_DIR=/tmp/tmp.olAMFPhcRe
#BASE_DIR=$(mktemp -d)
CASE=posix_meta
DIR=$BASE_DIR/$CASE

mkdir -p $DIR
touch $DIR/file1
ls -la $DIR
chown $USER:users $DIR/file1
ls -la $DIR
