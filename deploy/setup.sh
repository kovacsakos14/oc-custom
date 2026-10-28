#!/usr/bin/env bash
# Prepares this folder for docker compose: checks .env, fills in PUID/PGID,
# creates the folders, writes SWAG's site, with Authelia its secrets and the
# first user (admin), and with the management API its token. Safe to run
# again: it never replaces what exists.
#   ./setup.sh                     prepare (again)
#   ./setup.sh --new-manage-token  also replace the management API's token
set -euo pipefail
cd "$(dirname "$0")"
. ./lib.sh

new_manage_token=""
case "${1-}" in
    "") ;;
    --new-manage-token) new_manage_token=1 ;;
    *) die "Usage: ./setup.sh [--new-manage-token]" ;;
esac

for k in OC_STACK OC_DOMAIN OC_IMAGE SWAG_NETWORK; do
    [ -n "$(conf "$k")" ] || die "Set $k in .env"
done
# The admin password is needed until the first start (and Authelia's first
# user file); after that it can go from .env
first_start=""
[ -f config/opencloud.yaml ] || first_start=1
if [ -n "$first_start" ] || { with_authelia && [ ! -f "$USERS_FILE" ]; }; then
    admin_password="$(conf OC_ADMIN_PASSWORD)"
    [ -n "$admin_password" ] || die "Set OC_ADMIN_PASSWORD in .env"
    [ "${#admin_password}" -ge "$PASSWORD_MIN_LENGTH" ] ||
        die "OC_ADMIN_PASSWORD needs at least $PASSWORD_MIN_LENGTH characters"
fi
case "$(conf OC_STACK)" in *[!a-z0-9_-]*) die "OC_STACK: lowercase letters, digits, - and _ only" ;; esac
case "$(conf OC_DOMAIN)" in *[!A-Za-z0-9.-]*) die "OC_DOMAIN: a host name only, e.g. cloud.example.com" ;; esac
if [ -n "$(conf SMTP_HOST)" ]; then
    case "$(conf SMTP_ENCRYPTION)" in
        starttls|ssltls|none) ;;
        *) die "SMTP_ENCRYPTION: starttls, ssltls or none (is: '$(conf SMTP_ENCRYPTION)')" ;;
    esac
fi
if with_authelia; then
    for k in AUTHELIA_IMAGE ADMIN_EMAIL; do [ -n "$(conf "$k")" ] || die "Set $k in .env"; done
fi
# The management API's Authelia part goes with both or neither
case "$(conf COMPOSE_FILE)" in *compose.manage-authelia.yaml*) manage_authelia=1 ;; *) manage_authelia="" ;; esac
if with_manage && with_authelia && [ -z "$manage_authelia" ]; then
    die "COMPOSE_FILE: with Authelia, the management API needs compose.manage-authelia.yaml too"
fi
if [ -n "$manage_authelia" ] && ! { with_manage && with_authelia; }; then
    die "COMPOSE_FILE: compose.manage-authelia.yaml goes with compose.authelia.yaml and compose.manage.yaml"
fi
[ -z "$new_manage_token" ] || with_manage || die "--new-manage-token: no management API (COMPOSE_FILE in .env)"
chmod 600 .env

# The user running this owns everything; OpenCloud and Authelia run as it
for k in PUID PGID; do
    [ -z "$(conf "$k")" ] || continue
    v="$([ "$k" = PUID ] && id -u || id -g)"
    # the line from .env.example, or a new one when it isn't there
    if grep -q "^$k=" .env; then
        sed -i "s/^$k=.*/$k=$v/" .env
    else
        [ -z "$(tail -c 1 .env)" ] || echo >> .env # .env may not end in a newline
        echo "$k=$v" >> .env
    fi
    echo "$k=$v written to .env"
done

# Docker would create the branding mount point inside data/ as root
mkdir -p config data/web/assets/themes/_branding branding swag

# For OpenCloud's first start, which deletes it (compose.yaml)
if [ -n "$first_start" ]; then
    (umask 077; printf '%s' "$(conf OC_ADMIN_PASSWORD)" > config/.admin-password)
fi

docker network inspect "$(conf SWAG_NETWORK)" > /dev/null 2>&1 ||
    echo "Warning: no Docker network '$(conf SWAG_NETWORK)' (SWAG_NETWORK); docker network ls lists them" >&2

site="swag/$(conf OC_STACK).subdomain.conf"
sed -e "s/{{OC_STACK}}/$(conf OC_STACK)/g" -e "s/{{OC_DOMAIN}}/$(conf OC_DOMAIN)/g" \
    swag/opencloud.subdomain.conf.template > "$site"
echo "Wrote $site"

if with_authelia; then
    mkdir -p authelia/users authelia/data authelia/secrets authelia/assets
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
    # users.sh's lock; the management API's container mounts it (as a file,
    # so it has to exist first)
    touch authelia/.users.lock

    if [ ! -f "$USERS_FILE" ]; then
        # The admin, with OpenCloud's admin password and 2FA (group admins)
        export Y_USER=admin Y_NAME=Admin Y_EMAIL="$(conf ADMIN_EMAIL)"
        Y_HASH="$(hash_password "$(conf OC_ADMIN_PASSWORD)")"
        export Y_HASH
        [ -n "$Y_HASH" ] || die "Couldn't hash the admin's password"
        yq_start
        update_users '.users[strenv(Y_USER)] = {"displayname": strenv(Y_NAME),
            "email": strenv(Y_EMAIL), "password": strenv(Y_HASH), "disabled": false, "groups": ["admins"]}'
        echo "Wrote $USERS_FILE with admin (group admins: 2FA required)"
    fi
fi

# The management API's token: shown once, only its SHA-256 is kept (the API
# reads it at each request, so a new one works without a restart)
manage_token=""
# The API's records; Docker would create it as root
! with_manage || mkdir -p manage-api/state

if with_manage && { [ -n "$new_manage_token" ] || [ ! -s manage-api/token.sha256 ]; }; then
    mkdir -p manage-api
    api_image="$(conf MANAGE_API_IMAGE)"
    out="$(docker run --rm --entrypoint manage-api "${api_image:-$(conf OC_IMAGE)}" new-token)" ||
        die "Couldn't make the management API's token (is OC_IMAGE 7.2.4-9 or later, or MANAGE_API_IMAGE set?)"
    manage_token="$(sed -n 's/^token: //p' <<< "$out")"
    hash="$(sed -n 's/^sha256: //p' <<< "$out")"
    [ -n "$manage_token" ] && [ -n "$hash" ] || die "Couldn't make the management API's token"
    (umask 077; echo "$hash" > manage-api/token.sha256.new)
    mv manage-api/token.sha256.new manage-api/token.sha256
fi

cat <<EOF

Next:
1. Copy $site to SWAG's config/nginx/proxy-confs/ and restart SWAG.
2. docker compose up -d
3. Open https://$(conf OC_DOMAIN) and sign in as admin with OC_ADMIN_PASSWORD.
   Keep the password somewhere safe (a password manager), then remove it from
   .env: nothing reads it there after the first start.
EOF
if with_authelia; then
    cat <<EOF
   Authelia asks the admin to register a 2FA app first. The one-time code for
   that goes to ADMIN_EMAIL (without SMTP: authelia/data/notification.txt).
4. Users: ./users.sh help
EOF
fi
if [ -n "$manage_token" ]; then
    cat <<EOF

The management API's token (shown only now; enter it in your management
tool, with https://$(conf OC_DOMAIN)/manage-api/ as the address):

    $manage_token

A new one, which replaces it: ./setup.sh --new-manage-token
EOF
fi
