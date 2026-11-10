#!/bin/bash -eu

HTTP_HOST="$(snapctl get http.host)"
HTTP_PORT="$(snapctl get http.port)"
export HTTP_HOST HTTP_PORT

VERBOSE="$(snapctl get verbose)"
if [ "$VERBOSE" = "true" ]; then
    export LOG_LEVEL="debug"
else
    export LOG_LEVEL="info"
fi

exec "$@"
