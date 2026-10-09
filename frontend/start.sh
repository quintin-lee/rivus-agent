#!/bin/sh
set -e

export RIVUS_BACKEND="${RIVUS_BACKEND:-http://localhost:8080}"
envsubst '${RIVUS_BACKEND}' < /etc/nginx/conf.d/default.conf.template > /etc/nginx/conf.d/default.conf
exec nginx -g 'daemon off;'
