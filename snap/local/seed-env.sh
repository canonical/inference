#!/bin/bash -eu

HTTP_HOST="$(snapctl get http.host)"
HTTP_PORT="$(snapctl get http.port)"
export HTTP_HOST HTTP_PORT

exec "$@"
