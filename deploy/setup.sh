#!/usr/bin/env bash
# Prepares this folder for docker compose: checks .env, fills in PUID/PGID,
# creates the folders, writes SWAG's site, and with Authelia its secrets and
# the first user (admin). Safe to run again: it never replaces what exists.
set -euo pipefail
cd "$(dirname "$0")"
. ./lib.sh

for k in OC_STACK OC_DOMAIN OC_ADMIN_PASSWORD OC_IMAGE SWAG_NETWORK; do
    [ -n "$(conf "$k")" ] || die "Set $k in .env"
done
case "$(conf OC_STACK)" in *[!a-z0-9_-]*) die "OC_STACK: lowercase letters, digits, - and _ only" ;; esac
case "$(conf OC_DOMAIN)" in *[!A-Za-z0-9.-]*) die "OC_DOMAIN: a host name only, e.g. cloud.example.com" ;; esac
if with_authelia; then
    for k in AUTHELIA_IMAGE ADMIN_EMAIL; do [ -n "$(conf "$k")" ] || die "Set $k in .env"; done
fi
chmod 600 .env

# The user running this owns everything; OpenCloud and Authelia run as it
if [ -z "$(conf PUID)" ] || [ -z "$(conf PGID)" ]; then
    sed -i "s/^PUID=.*/PUID=$(id -u)/; s/^PGID=.*/PGID=$(id -g)/" .env
    echo "PUID=$(id -u), PGID=$(id -g) written to .env"
fi

# Docker would create the branding mount point inside data/ as root
mkdir -p config data/web/assets/themes/_branding branding swag

docker network inspect "$(conf SWAG_NETWORK)" > /dev/null 2>&1 ||
    echo "Warning: no Docker network '$(conf SWAG_NETWORK)' (SWAG_NETWORK); docker network ls lists them" >&2

site="swag/$(conf OC_STACK).subdomain.conf"
sed -e "s/{{OC_STACK}}/$(conf OC_STACK)/g" -e "s/{{OC_DOMAIN}}/$(conf OC_DOMAIN)/g" \
    swag/opencloud.subdomain.conf.template > "$site"
echo "Wrote $site"

if with_authelia; then
    mkdir -p authelia/users authelia/data authelia/secrets
    chmod 700 authelia/secrets authelia/data
    image="$(conf AUTHELIA_IMAGE)"
    for s in jwt session storage hmac; do
        [ -s "authelia/secrets/$s" ] && continue
        docker run --rm "$image" authelia crypto rand --length 64 --charset alphanumeric |
            sed -n 's/^Random Value: //p' > "authelia/secrets/$s"
        [ -s "authelia/secrets/$s" ] || die "Couldn't create authelia/secrets/$s"
    done
    if [ ! -s authelia/secrets/oidc.key ]; then
        docker run --rm -u "$(id -u):$(id -g)" -v "$PWD/authelia/secrets:/out" "$image" \
            authelia crypto pair rsa generate --bits 2048 --directory /out --file.private-key oidc.key --file.public-key oidc.pub > /dev/null
        rm -f authelia/secrets/oidc.pub
    fi
    chmod 600 authelia/secrets/*

    if [ ! -f "$USERS_FILE" ]; then
        # The admin, with OpenCloud's admin password and 2FA (group admins)
        export Y_USER=admin Y_NAME=Admin Y_EMAIL="$(conf ADMIN_EMAIL)"
        Y_HASH="$(hash_password "$(conf OC_ADMIN_PASSWORD)")"
        export Y_HASH
        [ -n "$Y_HASH" ] || die "Couldn't hash the admin's password"
        printf 'users: {}\n' | yq '.users[strenv(Y_USER)] = {"displayname": strenv(Y_NAME),
            "email": strenv(Y_EMAIL), "password": strenv(Y_HASH), "disabled": false, "groups": ["admins"]}' | write_users
        echo "Wrote $USERS_FILE with admin (group admins: 2FA required)"
    fi
fi

cat <<EOF

Next:
1. Copy $site to SWAG's config/nginx/proxy-confs/ and restart SWAG.
2. docker compose up -d
3. Open https://$(conf OC_DOMAIN) and sign in as admin with OC_ADMIN_PASSWORD.
EOF
if with_authelia; then
    cat <<EOF
   Authelia asks the admin to register a 2FA app first. The one-time code for
   that goes to ADMIN_EMAIL (without SMTP: authelia/data/notification.txt).
4. Users: ./users.sh help
EOF
fi
