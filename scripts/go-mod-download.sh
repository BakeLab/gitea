#!/bin/sh
set -eu

if [ -n "${BB_SDK_READ_TOKEN:-}" ]; then
	export GIT_CONFIG_COUNT=1
	export GIT_CONFIG_KEY_0=credential.helper
	export GIT_CONFIG_VALUE_0='!f() { echo username=x-access-token; echo "password=$BB_SDK_READ_TOKEN"; }; f'
fi

export GOPRIVATE=github.com/BakeLab/*
exec "${GO:-go}" mod download
