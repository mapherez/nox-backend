#!/bin/sh
set -eu
umask 077
config=/tmp/nox-suite-ingress.conf
: > "$config"
for cidr in $(printf '%s' "${NOX_FRONTEND_SUITE_TRUSTED_PROXY_CIDRS:-}" | tr ',' ' '); do
 case "$cidr" in *[!0-9a-fA-F:./]*) printf '%s\n' 'Invalid Suite trusted ingress CIDR' >&2; exit 1;; esac
 printf '%s 1;\n' "$cidr" >> "$config"
done
# Nginx validates exact CIDR syntax when loading the generated geo include.
