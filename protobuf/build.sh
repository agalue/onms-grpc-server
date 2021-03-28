#!/bin/bash

type protoc >/dev/null 2>&1 || { echo >&2 "protoc required but it's not installed; aborting."; exit 1; }

protoc --proto_path=./proto --go_out=./ sink-message.proto
protoc --proto_path=./proto --go_out=./ kafka-rpc.proto
protoc --proto_path=./proto --go_out=plugins=grpc:./ ipc.proto
