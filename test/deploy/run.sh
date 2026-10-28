#!/usr/bin/env bash
# Tests deploy/ end to end on this machine, as on a server behind SWAG: the
# real SWAG image (with a self-signed certificate, so Let's Encrypt is
# skipped), Mailpit for the e-mails, and deploy/ copied twice: with Authelia,
# then without it. Everything runs on its own Docker network and is removed
# afterwards. Needs Docker; about 10 minutes.
#   test/deploy/run.sh [--image IMAGE] [--manage-api-image IMAGE] [--keep]
#     --image  the oc-custom image (default: the one versions.env builds)
#     --manage-api-image  another image for the Authelia stack's management
#              API (MANAGE_API_IMAGE; default: the one in the oc-custom image)
#     --keep   leave everything running (remove it with --clean)
#   test/deploy/run.sh --clean
set -euo pipefail
repo="$(cd "$(dirname "$0")/../.." && pwd)"
here="$repo/test/deploy"
# shellcheck source=../../versions.env
source "$repo/versions.env"

IMAGE="ghcr.io/kovacsakos14/oc-custom:${OC_VERSION#v}-$BUILD"
API_IMAGE=""
KEEP=""
while [ $# -gt 0 ]; do
    case "$1" in
        --image) IMAGE="$2"; shift 2 ;;
        --manage-api-image) API_IMAGE="$2"; shift 2 ;;
        --keep) KEEP=1; shift ;;
        --clean) KEEP=clean; shift ;;
        *) sed -n '2,13p' "$0" >&2; exit 1 ;;
    esac
done

SWAG_IMAGE=lscr.io/linuxserver/swag:5.8.0-ls493@sha256:c672b9ed0dcaabad0aad95a965493be0b3483673cd5f2e3d2105828be6603bde
MAILPIT_IMAGE=axllent/mailpit:v1.31.6@sha256:23fe39e81bde596a918000b3135c0f85dc8bb4c9ea6be274b051143f3e300387
CURL_IMAGE=curlimages/curl:8.17.0@sha256:935d9100e9ba842cdb060de42472c7ca90cfe9a7c96e4dacb55e79e560b3ff40
CHROME_IMAGE=zenika/alpine-chrome@sha256:ee10e24217aa27443e6b58da628f3b09ea9b814459915b8b62fe15a555f9692a
WORK="$here/work"
NET=oc-custom-deploy-test
DOMAIN=cloud.deploy.test   # with Authelia
DOMAIN2=plain.deploy.test  # without
ADMIN_PASSWORD='Admin-pass1'

fails=0
# Each result shows how long its check or step took; T0 is set when it starts
T0=$SECONDS
ok() { printf '  ok    %4ss  %s\n' "$((SECONDS - T0))" "$*"; }
bad() { printf '  FAIL  %4ss  %s\n' "$((SECONDS - T0))" "$*"; fails=$((fails + 1)); }
check() { # description, shell code (run with this script's functions)
    T0=$SECONDS
    if eval "$2" > /dev/null 2>&1; then ok "$1"; else bad "$1"; fi
}

cleanup() {
    for s in a b; do
        [ -f "$WORK/$s/.env" ] && (cd "$WORK/$s" && docker compose down --remove-orphans > /dev/null 2>&1) || true
    done
    docker rm -f dtest-swag dtest-mailpit > /dev/null 2>&1 || true
    docker network rm "$NET" > /dev/null 2>&1 || true
    # some files are owned by the containers' users
    [ -d "$WORK" ] && docker run --rm -v "$here:/h" alpine:3.24 rm -rf /h/work
    return 0
}
if [ "$KEEP" = clean ]; then cleanup; echo "Removed"; exit 0; fi
cleanup
[ -n "$KEEP" ] || trap cleanup EXIT

curl_() { docker run --rm --network "$NET" "$CURL_IMAGE" -sk "$@"; }
browser() { # script, env...
    local s="$1"; shift
    local envs=(-e OC_DOMAIN="$DOMAIN" -e MAILPIT=dtest-mailpit) # later -e win
    for e in "$@"; do envs+=(-e "$e"); done
    docker run --rm --user 0 --network "$NET" "${envs[@]}" \
        -v "$here/e2e:/usr/src/app/s:ro" -v "$WORK/shots:/out" \
        --entrypoint node "$CHROME_IMAGE" "s/$s" 2>&1 | grep -v -i 'NODE_TLS\|trace-warnings' | sed 's/^/        /'
    return "${PIPESTATUS[0]}"
}
step() { # description, browser script, env...
    local d="$1"; shift
    echo "  ..    $d"
    T0=$SECONDS
    if browser "$@"; then ok "$d"; else bad "$d"; fi
}

# A copy of deploy/ as a server would have it: stack name, domain, Authelia yes/no
stack() {
    local dir="$WORK/$1" name="$2" domain="$3" authelia="$4"
    cp -r "$repo/deploy" "$dir"
    cp "$here/compose.test.yaml" "$here/compose.test-authelia.yaml" "$dir/"
    local files="compose.yaml:compose.authelia.yaml:compose.manage.yaml:compose.manage-authelia.yaml:compose.test.yaml:compose.test-authelia.yaml"
    [ "$authelia" = yes ] || files="compose.yaml:compose.manage.yaml:compose.test.yaml"
    sed -e "s/^OC_STACK=.*/OC_STACK=$name/" -e "s/^OC_DOMAIN=.*/OC_DOMAIN=$domain/" \
        -e "s/^OC_ADMIN_PASSWORD=.*/OC_ADMIN_PASSWORD=$ADMIN_PASSWORD/" \
        -e "s/^ADMIN_EMAIL=.*/ADMIN_EMAIL=admin@deploy.test/" -e "s|^OC_IMAGE=.*|OC_IMAGE=$IMAGE|" \
        -e "s|^COMPOSE_FILE=.*|COMPOSE_FILE=$files|" -e "s/^SWAG_NETWORK=.*/SWAG_NETWORK=$NET/" \
        -e "s/^SMTP_HOST=.*/SMTP_HOST=dtest-mailpit/" -e "s/^SMTP_PORT=.*/SMTP_PORT=1025/" \
        -e "s/^SMTP_ENCRYPTION=.*/SMTP_ENCRYPTION=none/" -e "s/^SMTP_AUTHENTICATION=.*/SMTP_AUTHENTICATION=none/" \
        "$dir/.env.example" > "$dir/.env"
    if [ "$authelia" = yes ] && [ -n "$API_IMAGE" ]; then
        sed -i "s|^#MANAGE_API_IMAGE=.*|MANAGE_API_IMAGE=$API_IMAGE|" "$dir/.env"
    fi
    # The management API's token is shown once, indented on its own line
    (cd "$dir" && ./setup.sh) | awk '/shown only now/ { f = 1 } f && /^    [A-Za-z0-9_-]{43}$/ { print $1; exit }' > "$dir/.manage-token"
    [ -s "$dir/.manage-token" ] || { echo "setup.sh showed no management API token" >&2; exit 1; }
    cp "$dir/swag/$name.subdomain.conf" "$WORK/swag/nginx/proxy-confs/"
}

wait_up() { # domain, with Authelia
    local i
    for i in $(seq 90); do
        if curl_ "https://$1/status.php" | grep -q '"installed": *true'; then
            [ "$2" = no ] || curl_ "https://$1/authelia/api/health" | grep -q OK && return 0
        fi
        sleep 2
    done
    return 1
}

user_token() { # user: a short-lived app token, as users.sh makes admin's
    docker compose exec -T opencloud opencloud auth-app create --user-name "$1" --expiration 10m | sed -n 's/^ *token: //p'
}
admin_token() { user_token admin; }
YQ_IMAGE="$(sed -n 's/^YQ_IMAGE=//p' "$repo/deploy/lib.sh")"
yq_() { docker run --rm -i -e Y_ID "$YQ_IMAGE" -p json "$@"; } # Y_ID: for strenv() in the container
personal_state() { # drive id: its state as the admin sees it (trashed when disabled)
    curl_ -u "admin:$(admin_token)" "https://$DOMAIN/graph/v1.0/drives?%24filter=driveType%20eq%20%27personal%27" |
        Y_ID="$1" yq_ '.value[] | select(.id == strenv(Y_ID)) | (.root.deleted.state // "enabled")'
}
link_opens() { # public link token: its folder opens with the link's password (207)
    [ "$(curl_ -u "public:Link-Pass-1234!" -X PROPFIND -H "Depth: 0" -o /dev/null -w "%{http_code}" \
        "https://$DOMAIN/dav/public-files/$1")" = 207 ]
}

# The management API of the stack in the current folder, through SWAG
mapi() { # METHOD PATH [JSON]: the answer, then its HTTP status on a line of its own
    local args=(-X "$1" -H "Authorization: Bearer $(cat .manage-token)" -w '\n%{http_code}')
    [ -z "${3-}" ] || args+=(-H "Content-Type: application/json" -d "$3")
    curl_ "${args[@]}" "https://$(sed -n 's/^OC_DOMAIN=//p' .env)/manage-api$2"
}
mcode() { mapi "$@" | tail -n 1; } # the status only
mbody() { mapi "$@" | sed '$d'; }  # the answer only
firstfactor() { # user, password: Authelia accepts them
    curl_ -H "Content-Type: application/json" -d "{\"username\":\"$1\",\"password\":\"$2\"}" \
        "https://$DOMAIN/authelia/api/firstfactor" | grep -q '"status":"OK"'
}

logon() { # domain, user, password: the built-in sign-in's answer (200 ok, 204 refused)
    curl_ -o /dev/null -w '%{http_code}' -H 'Kopano-Konnect-XSRF: 1' -H "Referer: https://$1/signin/v1/identifier" \
        -H "Origin: https://$1" -H 'Content-Type: application/json' "https://$1/signin/v1/identifier/_/logon" \
        -d "{\"params\":[\"$2\",\"$3\",\"1\"],\"hello\":{\"scope\":\"openid profile\",\"client_id\":\"web\",\"redirect_uri\":\"https://$1/oidc-callback.html\",\"flow\":\"oidc\"}}"
}

echo "== Setup: oc-custom $IMAGE, $AUTHELIA_IMAGE, SWAG ${SWAG_IMAGE%@*}"
check "deploy/.env.example names versions.env's Authelia" \
    '[ "$(sed -n "s/^AUTHELIA_IMAGE=//p" "$repo/deploy/.env.example")" = "$AUTHELIA_IMAGE" ]'
mkdir -p "$WORK/shots" "$WORK/swag/nginx/proxy-confs" "$WORK/swag/etc/letsencrypt/live/$DOMAIN"
# SWAG skips Let's Encrypt when a certificate is already there
openssl req -x509 -newkey rsa:2048 -nodes -days 7 -subj "/CN=$DOMAIN" \
    -addext "subjectAltName=DNS:$DOMAIN,DNS:$DOMAIN2" \
    -keyout "$WORK/swag/etc/letsencrypt/live/$DOMAIN/privkey.pem" \
    -out "$WORK/swag/etc/letsencrypt/live/$DOMAIN/fullchain.pem" 2> /dev/null
docker network create "$NET" > /dev/null
stack a dtest "$DOMAIN" yes
stack b dtest2 "$DOMAIN2" no
docker run -d --name dtest-mailpit --network "$NET" "$MAILPIT_IMAGE" > /dev/null
docker run -d --name dtest-swag --network "$NET" --network-alias "$DOMAIN" --network-alias "$DOMAIN2" \
    -e PUID="$(id -u)" -e PGID="$(id -g)" -e TZ=Etc/UTC -e URL=deploy.test -e SUBDOMAINS=cloud,plain \
    -e ONLY_SUBDOMAINS=true -e VALIDATION=http -v "$WORK/swag:/config" "$SWAG_IMAGE" > /dev/null
for i in $(seq 60); do docker logs dtest-swag 2>&1 | grep -q 'Server ready' && break; sleep 2; done
check "setup.sh refuses an unknown SMTP_ENCRYPTION" \
    '(cd "$WORK/a" && sed -i.bak "s/^SMTP_ENCRYPTION=.*/SMTP_ENCRYPTION=tls/" .env; ! ./setup.sh; s=$?; mv .env.bak .env; exit $s)'
check "SWAG started without Let's Encrypt and accepts both sites (nginx -t)" 'docker exec dtest-swag nginx -t'

echo "== With Authelia ($DOMAIN), at ${SECONDS}s"
cd "$WORK/a"
check "docker compose up -d" "docker compose up -d"
check "OpenCloud and Authelia answer through SWAG" 'wait_up "$DOMAIN" yes'
check "the admin password isn't in OpenCloud's environment, and its one-time file is gone" \
    '! docker inspect dtest-opencloud --format "{{json .Config.Env}}" | grep -qF "$ADMIN_PASSWORD" && [ ! -e config/.admin-password ]'
check "OpenCloud's sign-in configuration is Authelia's" \
    'curl_ "https://$DOMAIN/.well-known/openid-configuration" | grep -q "\"issuer\":\"https://$DOMAIN/authelia\""'
check "Authelia logs the client's address, not one the client sent (X-Forwarded-For)" \
    'curl_ -H "X-Forwarded-For: 6.6.6.6" -H "Content-Type: application/json" -d "{\"username\":\"xff-probe\",\"password\":\"x\"}" \
        "https://$DOMAIN/authelia/api/firstfactor" > /dev/null; sleep 1
     lines="$(docker compose logs authelia | grep xff-probe)"
     grep -q remote_ip <<< "$lines" && ! grep -q 6.6.6.6 <<< "$lines"'
check "OpenCloud runs notifications (SMTP set) and not its own sign-in (idp)" \
    'services="$(docker compose exec -T opencloud opencloud list)"; grep -q " notifications " <<< "$services" && ! grep -q " idp " <<< "$services"'
check "users.sh email admin" './users.sh email admin admin@deploy.test'
check "SWAG adds the \"Back to your files\" script to Authelia's pages and serves it" \
    'curl_ "https://$DOMAIN/authelia/settings" | grep -qF "<script src=\"/authelia-back.js\" defer></script></head>" &&
     curl_ "https://$DOMAIN/authelia-back.js" | grep -qF "Back to your files"'
check "Authelia has no OIDC sign-out endpoint; the web signs out at its /logout (config.json logoutUrl)" \
    '! curl_ "https://$DOMAIN/.well-known/openid-configuration" | grep -q end_session_endpoint &&
     curl_ "https://$DOMAIN/config.json" | grep -Eq "\"logoutUrl\": ?\"https://$DOMAIN/authelia/logout\\?rd="'
check "no Calendar (capability support-radicale off)" \
    'curl_ -u "admin:$(admin_token)" "https://$DOMAIN/ocs/v1.php/cloud/capabilities?format=json" | grep -Eq "\"support-radicale\": ?false"'
check "users.sh add refuses a password that's too short" '! echo ab | ./users.sh add shorty'
check "users.sh add alice (password on stdin)" 'echo Alice-pass1 | ./users.sh add alice --name "Alice Example" --email alice@deploy.test'
check "impersonation is off: admin's token can't make one for alice" \
    '[ "$(curl_ -u "admin:$(admin_token)" -X POST -o /dev/null -w "%{http_code}" \
        "https://$DOMAIN/auth-app/tokens?expiry=10m&userName=alice")" -ge 400 ]'
check "users.sh add bob as User Light (--random)" './users.sh add bob --role light --email bob@deploy.test --random < /dev/null'
check "users.sh disable bob" './users.sh disable bob'
check "two users.sh add at the same time both land in Authelia's file" \
    'echo Gina-pass1 | ./users.sh add gina & p=$!; echo Hank-pass1 | ./users.sh add hank && wait $p &&
     grep -q "^  gina:" authelia/users/users_database.yml && grep -q "^  hank:" authelia/users/users_database.yml &&
     ./users.sh delete gina < /dev/null && ./users.sh delete hank < /dev/null'
check "users.sh add erin as Client (--random)" './users.sh add erin --role client --email erin@deploy.test --random < /dev/null'
check "users.sh add fred as Employee (--random)" './users.sh add fred --role employee --email fred@deploy.test --random < /dev/null'
list="$(./users.sh list)"
echo "$list" | sed 's/^/        /'
check "list: alice user, bob light and disabled on both sides" \
    'grep -Eq "^alice .* user +yes +ok$" <<< "$list" && grep -Eq "^bob .* light +no +disabled$" <<< "$list"'
check "list: erin client, fred employee" \
    'grep -Eq "^erin .* client +yes +ok$" <<< "$list" && grep -Eq "^fred .* employee +yes +ok$" <<< "$list"'
check "users.sh role erin employee, then client again" \
    './users.sh role erin employee && ./users.sh list | grep -Eq "^erin .* employee " && ./users.sh role erin client'
step "alice: password only, Profile without Edit, Change password on Authelia's page, Log out ends Authelia's session" user.js \
    USER_NAME=alice USER_PASSWORD=Alice-pass1 USER_NEW_PASSWORD=Alice-pass2 USER_EMAIL=alice@deploy.test
step "bob (disabled) can't sign in" disabled.js USER_NAME=bob USER_PASSWORD=whatever
step "the desktop app's sign-in (PKCE, loopback redirect, refresh)" desktop.js USER_NAME=alice USER_PASSWORD=Alice-pass2
step "admin: 2FA registered at the first sign-in, removed and registered again on the settings page" admin-2fa.js \
    ADMIN_PASSWORD="$ADMIN_PASSWORD" ADMIN_EMAIL=admin@deploy.test
check "users.sh reset-2fa admin" './users.sh reset-2fa admin'
step "admin is asked to register 2FA again" twofa.js USER_NAME=admin USER_PASSWORD="$ADMIN_PASSWORD" WANT=register
# Changing a User to a type without a personal space disables it, with the links from it
check "alice (User) shares a folder in her personal space by a public link" \
    'A="alice:$(user_token alice)"
     space="$(curl_ -u "$A" "https://$DOMAIN/graph/v1.0/me/drives?%24filter=driveType%20eq%20%27personal%27" | yq_ ".value[0].id")"
     curl_ -u "$A" -X MKCOL "https://$DOMAIN/dav/spaces/$space/linked" &&
     item="$(curl_ -u "$A" -X PROPFIND -H "Depth: 0" "https://$DOMAIN/dav/spaces/$space/linked" \
        -d "<d:propfind xmlns:d=\"DAV:\" xmlns:oc=\"http://owncloud.org/ns\"><d:prop><oc:fileid/></d:prop></d:propfind>" |
        sed -n "s|.*<oc:fileid>\([^<]*\)</oc:fileid>.*|\1|p")" &&
     link="$(curl_ -u "$A" -H "Content-Type: application/json" -d "{\"type\":\"view\",\"password\":\"Link-Pass-1234!\"}" \
        "https://$DOMAIN/graph/v1beta1/drives/${space//\$/%24}/items/${item//\$/%24}/createLink" | yq_ ".link.webUrl")" &&
     link="${link##*/}" && [ -n "$link" ] && link_opens "$link"'
check "users.sh role alice client disables her personal space; the link stops working" \
    './users.sh role alice client < /dev/null && [ "$(personal_state "$space")" = trashed ] && ! link_opens "$link"'
check "alice can't enable her personal space herself (patches/reva/0002)" \
    '[ "$(curl_ -u "$A" -X PATCH -H "Restore: T" -H "Content-Type: application/json" -d "{}" -o /dev/null -w "%{http_code}" \
        "https://$DOMAIN/graph/v1.0/drives/${space//\$/%24}")" -ge 400 ] && [ "$(personal_state "$space")" = trashed ]'
check "users.sh role alice user enables it again; the link works again" \
    './users.sh role alice user < /dev/null && [ "$(personal_state "$space")" = enabled ] && link_opens "$link"'
admins() { grep -A8 "^  $1:" authelia/users/users_database.yml | grep -q -- '- admins'; }
check "users.sh role alice admin puts her in admins" './users.sh role alice admin && admins alice'
check "users.sh role alice user takes her out" './users.sh role alice user && ! admins alice'
check "users.sh refuses the usernames . and .." '! ./users.sh disable .. && ! ./users.sh disable .'
check "users.sh role alice boss is refused" '! ./users.sh role alice boss'
check "users.sh delete bob (both sides)" \
    './users.sh delete bob < /dev/null && ! ./users.sh list | grep -q "^bob " && ! grep -q "^  bob:" authelia/users/users_database.yml'
check "users.sh delete erin and fred" './users.sh delete erin < /dev/null && ./users.sh delete fred < /dev/null'
# A user made in OpenCloud only (its web UI, or before Authelia): passwd adds them to Authelia
check "a user created in OpenCloud only gets an Authelia entry from users.sh passwd" \
    'curl_ -u "admin:$(admin_token)" -H "Content-Type: application/json" -o /dev/null -w "%{http_code}" \
        -d "{\"onPremisesSamAccountName\":\"dave\",\"displayName\":\"Dave\",\"mail\":\"dave@deploy.test\",\"passwordProfile\":{\"password\":\"x-unused-1\"}}" \
        "https://$DOMAIN/graph/v1.0/users" | grep -q 201 && echo Dave-pass1 | ./users.sh passwd dave &&
     curl_ -H "Content-Type: application/json" -d "{\"username\":\"dave\",\"password\":\"Dave-pass1\"}" \
        "https://$DOMAIN/authelia/api/firstfactor" | grep -q "\"status\":\"OK\""'
check "users.sh leaves no app token on disk" '[ ! -e .admin-token ]'
check "users.sh revokes its app token when it's done" \
    'T="$(admin_token)"; n="$(curl_ -u "admin:$T" "https://$DOMAIN/auth-app/tokens" | yq_ length)" &&
     ./users.sh list > /dev/null && [ "$(curl_ -u "admin:$T" "https://$DOMAIN/auth-app/tokens" | yq_ length)" = "$n" ]'
check "the user file is private (600)" '[ "$(stat -c %a authelia/users/users_database.yml)" = 600 ]'

echo "  --    the management API (with Authelia), at ${SECONDS}s"
check "the API runs from MANAGE_API_IMAGE when it's set, else from OC_IMAGE" \
    '[ "$(docker inspect -f "{{.Config.Image}}" "$(docker compose ps -q manage-api)")" = "${API_IMAGE:-$IMAGE}" ]'
check "setup.sh stored only the token's SHA-256 (600)" \
    '[ "$(stat -c %a manage-api/token.sha256)" = 600 ] && ! grep -qF "$(cat .manage-token)" manage-api/token.sha256'
check "the API refuses a missing or wrong token (401)" \
    '[ "$(curl_ -o /dev/null -w "%{http_code}" "https://$DOMAIN/manage-api/v1/info")" = 401 ] &&
     [ "$(curl_ -o /dev/null -w "%{http_code}" -H "Authorization: Bearer wrong" "https://$DOMAIN/manage-api/v1/info")" = 401 ]'
check "info through SWAG: OpenCloud's version and Authelia" \
    'mbody GET /v1/info | grep -q "\"opencloud\":\"$(curl_ "https://$DOMAIN/status.php" | sed -n "s/.*\"productversion\": *\"\([^\"]*\)\".*/\1/p")\",\"authelia\":true"'
# A Client made in OpenCloud only (as a management tool does), then its Authelia entry through the API
check "a Client created in OpenCloud gets its Authelia entry through the API and signs in" \
    '[ "$(curl_ -u "admin:$(admin_token)" -H "Content-Type: application/json" -o /dev/null -w "%{http_code}" \
        -d "{\"onPremisesSamAccountName\":\"ivy\",\"displayName\":\"Ivy\",\"mail\":\"ivy@deploy.test\",\"passwordProfile\":{\"password\":\"x-unused-1\"}}" \
        "https://$DOMAIN/graph/v1.0/users")" = 201 ] && ./users.sh role ivy client < /dev/null > /dev/null &&
     mbody PUT /v1/authelia/users/ivy "{\"displayname\":\"Ivy\",\"email\":\"ivy@deploy.test\",\"password\":\"Ivy-pass1\"}" | grep -q "\"created\":true" &&
     for i in 1 2 3 4 5; do firstfactor ivy Ivy-pass1 && break; sleep 1; done && [ "$i" -lt 5 ]'
check "the API's list: ivy without a hash, admin marked" \
    'l="$(mbody GET /v1/authelia/users)"; grep -q "\"name\":\"ivy\"" <<< "$l" && grep -q "\"name\":\"admin\",[^}]*\"admin\":true" <<< "$l" && ! grep -q argon2 <<< "$l"'
check "disabled through the API, Authelia refuses ivy; enabled, she's back" \
    '[ "$(mcode PUT /v1/authelia/users/ivy "{\"disabled\":true}")" = 200 ] && sleep 2 && ! firstfactor ivy Ivy-pass1 &&
     [ "$(mcode PUT /v1/authelia/users/ivy "{\"disabled\":false}")" = 200 ] && sleep 2 && firstfactor ivy Ivy-pass1'
check "a new password through the API: the new one works, the old one doesn't" \
    '[ "$(mcode PUT /v1/authelia/users/ivy "{\"password\":\"Ivy-pass2\"}")" = 200 ] && sleep 2 &&
     firstfactor ivy Ivy-pass2 && ! firstfactor ivy Ivy-pass1'
check "users.sh 2fa ivy on (the 2fa group)" \
    './users.sh 2fa ivy on < /dev/null && grep -A8 "^  ivy:" authelia/users/users_database.yml | grep -q -- "- 2fa"'
step "ivy (a Client in the 2fa group) is asked to register a 2FA app" twofa.js USER_NAME=ivy USER_PASSWORD=Ivy-pass2 WANT=register
check "2FA off for ivy through the API (two_factor false; the list shows it)" \
    '[ "$(mcode PUT /v1/authelia/users/ivy "{\"two_factor\":false}")" = 200 ] &&
     mbody GET /v1/authelia/users | grep -q "\"name\":\"ivy\",[^}]*\"two_factor\":false"'
step "ivy signs in with the password only again" twofa.js USER_NAME=ivy USER_PASSWORD=Ivy-pass2 WANT=files
check "2FA on for ivy through the API; info says the configuration has the 2fa rule" \
    '[ "$(mcode PUT /v1/authelia/users/ivy "{\"two_factor\":true}")" = 200 ] &&
     mbody GET /v1/authelia/users | grep -q "\"name\":\"ivy\",[^}]*\"two_factor\":true" &&
     mbody GET /v1/info | grep -q "\"two_factor_group\":true"'
step "ivy is asked to register a 2FA app again" twofa.js USER_NAME=ivy USER_PASSWORD=Ivy-pass2 WANT=register
check "users.sh 2fa admin off is refused (admins always need 2FA)" '! ./users.sh 2fa admin off < /dev/null'
check "Reset 2FA through the API (Authelia's CLI in the API's container)" \
    'mbody POST /v1/authelia/users/ivy/reset-2fa | grep -q "\"reset\":true"'
check "ivy's OpenCloud e-mails muted and unmuted through the API, impersonation off" \
    'mbody PUT /v1/opencloud/users/ivy/emails "{\"disabled\":true}" | grep -q "\"disabled\":true" &&
     mbody GET /v1/opencloud/users/ivy/emails | grep -q "\"disabled\":true" &&
     mbody PUT /v1/opencloud/users/ivy/emails "{\"disabled\":false}" | grep -q "\"disabled\":false"'
check "the API left no app token of ivy's behind" \
    '[ "$(curl_ -u "ivy:$(user_token ivy)" "https://$DOMAIN/auth-app/tokens" | yq_ length)" = 1 ]'
check "a User's personal space through the API (alice's); a Client gets none (409)" \
    'mbody POST /v1/opencloud/users/alice/personal-space | grep -qF "\"id\":\"$space\"" &&
     [ "$(mcode POST /v1/opencloud/users/ivy/personal-space)" = 409 ]'
check "the API refuses every change to admin (403); admin's entry is unchanged" \
    'before="$(grep -A6 "^  admin:" authelia/users/users_database.yml)" &&
     [ "$(mcode PUT /v1/authelia/users/admin "{\"password\":\"Taken-over1\"}")" = 403 ] &&
     [ "$(mcode PUT /v1/authelia/users/admin "{\"disabled\":true}")" = 403 ] &&
     [ "$(mcode PUT /v1/authelia/users/admin "{\"two_factor\":false}")" = 403 ] &&
     [ "$(mcode POST /v1/authelia/users/admin/reset-2fa)" = 403 ] &&
     [ "$(mcode DELETE /v1/authelia/users/admin)" = 403 ] &&
     [ "$(mcode PUT /v1/opencloud/users/admin/emails "{\"disabled\":true}")" = 403 ] &&
     [ "$before" = "$(grep -A6 "^  admin:" authelia/users/users_database.yml)" ]'
# OpenCloud's role only (Graph), Authelia's file untouched
oc_role() { # user, role id
    local T id app
    T="admin:$(admin_token)"
    id="$(curl_ -u "$T" "https://$DOMAIN/graph/v1.0/users/$1" | yq_ .id)"
    app="$(curl_ -u "$T" "https://$DOMAIN/graph/v1.0/applications" | yq_ ".value[0].id")"
    [ "$(curl_ -u "$T" -H "Content-Type: application/json" -o /dev/null -w "%{http_code}" \
        -d "{\"principalId\":\"$id\",\"resourceId\":\"$app\",\"appRoleId\":\"$2\"}" \
        "https://$DOMAIN/graph/v1.0/users/$id/appRoleAssignments")" = 201 ]
}
check "an OpenCloud Admin outside Authelia's admins group is refused too" \
    'oc_role ivy 71881883-1768-46bd-a24d-a356a2afdf7f && ! grep -A8 "^  ivy:" authelia/users/users_database.yml | grep -q -- "- admins" &&
     c="$(mcode PUT /v1/authelia/users/ivy "{\"password\":\"Taken-over1\"}")"; oc_role ivy 38f0649f-b600-4188-b138-fbd2c773cc43 && [ "$c" = 403 ]'
check "users.sh and the API changing the user file at the same time: both land" \
    'echo Gina-pass1 | ./users.sh add gina > /dev/null & p=$!
     c="$(mcode PUT /v1/authelia/users/hank "{\"displayname\":\"Hank\",\"password\":\"Hank-pass1\"}")"; wait $p && [ "$c" = 200 ] &&
     grep -q "^  gina:" authelia/users/users_database.yml && grep -q "^  hank:" authelia/users/users_database.yml &&
     ./users.sh delete gina < /dev/null > /dev/null && [ "$(mcode DELETE /v1/authelia/users/hank)" = 200 ] &&
     ! grep -q "^  hank:" authelia/users/users_database.yml'
check "the API comes back after OpenCloud's container restarts (its network namespace)" \
    'docker restart dtest-opencloud > /dev/null && wait_up "$DOMAIN" yes &&
     for i in $(seq 40); do [ "$(mcode GET /v1/info)" = 200 ] && break; sleep 3; done && [ "$i" -lt 40 ]'
check "users.sh delete ivy" './users.sh delete ivy < /dev/null'
docker compose down > /dev/null 2>&1

echo "== Without Authelia ($DOMAIN2), at ${SECONDS}s"
cd "$WORK/b"
check "docker compose up -d" "docker compose up -d"
check "OpenCloud answers through SWAG" 'wait_up "$DOMAIN2" no'
check "no Authelia container; OpenCloud runs its own sign-in (idp)" \
    '! docker compose ps --services | grep -q authelia && docker compose exec -T opencloud opencloud list | grep -q " idp "'
# the sign-in service may still be starting right after the first answer
check "admin signs in with the password from the first start (200)" \
    'for i in $(seq 15); do [ "$(logon "$DOMAIN2" admin "$ADMIN_PASSWORD")" = 200 ] && break; sleep 2; done; [ "$i" -lt 15 ] || [ "$(logon "$DOMAIN2" admin "$ADMIN_PASSWORD")" = 200 ]'
check "the web gets no password link (config.json)" '! curl_ "https://$DOMAIN2/config.json" | grep -q accountEditLink'
check "users.sh add carol" 'echo Carol-pass1 | ./users.sh add carol --email carol@deploy.test'
check "carol signs in on OpenCloud's own sign-in (200)" '[ "$(logon "$DOMAIN2" carol Carol-pass1)" = 200 ]'
check "users.sh passwd carol" 'echo Carol-pass2 | ./users.sh passwd carol'
check "the new password works, the old one doesn't (200/204)" \
    '[ "$(logon "$DOMAIN2" carol Carol-pass2)" = 200 ] && [ "$(logon "$DOMAIN2" carol Carol-pass1)" = 204 ]'
check "users.sh reset-2fa is refused without Authelia" '! ./users.sh reset-2fa carol'
step "carol: OpenCloud's own sign-in page, Change password opens its dialog" builtin.js \
    OC_DOMAIN="$DOMAIN2" USER_NAME=carol USER_PASSWORD=Carol-pass2
echo "  --    the management API (without Authelia), at ${SECONDS}s"
check "info: no Authelia; its endpoints answer 404" \
    'mbody GET /v1/info | grep -q "\"authelia\":false" && [ "$(mcode GET /v1/authelia/users)" = 404 ] &&
     [ "$(mcode PUT /v1/authelia/users/carol "{\"disabled\":true}")" = 404 ]'
check "carol's e-mails muted and unmuted, her personal space through the API" \
    'mbody PUT /v1/opencloud/users/carol/emails "{\"disabled\":true}" | grep -q "\"disabled\":true" &&
     mbody PUT /v1/opencloud/users/carol/emails "{\"disabled\":false}" | grep -q "\"disabled\":false" &&
     mbody POST /v1/opencloud/users/carol/personal-space | grep -q "\"id\":\"[^\"]"'
check "admin is refused here too (403)" '[ "$(mcode PUT /v1/opencloud/users/admin/emails "{\"disabled\":true}")" = 403 ]'
check "setup.sh --new-manage-token: the new token works at once, the old one is refused" \
    'old="$(cat .manage-token)" && ./setup.sh --new-manage-token | awk "/shown only now/ { f = 1 } f && /^    [A-Za-z0-9_-]{43}\$/ { print \$1; exit }" > .manage-token &&
     [ -s .manage-token ] && [ "$(cat .manage-token)" != "$old" ] && [ "$(mcode GET /v1/info)" = 200 ] &&
     [ "$(curl_ -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $old" "https://$DOMAIN2/manage-api/v1/info")" = 401 ]'
docker compose down > /dev/null 2>&1

echo
echo "Took ${SECONDS}s"
if [ "$fails" = 0 ]; then echo "All checks passed"; else echo "$fails check(s) failed; screenshots in $WORK/shots (with --keep)"; fi
[ "$fails" = 0 ]
