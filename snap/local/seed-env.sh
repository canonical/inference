#!/bin/bash -eu

HTTP_HOST="$(snapctl get http.host)"
HTTP_PORT="$(snapctl get http.port)"
HTTP_CORS_ALLOW_ALL="$(snapctl get http.cors-allow-all)"
export HTTP_HOST HTTP_PORT HTTP_CORS_ALLOW_ALL

VERBOSE="$(snapctl get verbose)"
if [ "$VERBOSE" = "true" ]; then
    export LOG_LEVEL="debug"
else
    export LOG_LEVEL="info"
fi

exec "$@"
