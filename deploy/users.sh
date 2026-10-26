#!/usr/bin/env bash
# Manages users on this server: OpenCloud's accounts and, with Authelia,
# their passwords, groups and 2FA in Authelia. Run ./users.sh help.
set -euo pipefail
cd "$(dirname "$0")"
. ./lib.sh

ROLE_ADMIN=71881883-1768-46bd-a24d-a356a2afdf7f
ROLE_USER=d7beeea8-8ff4-406b-8fb6-ab2dd81e6b11
ROLE_LIGHT=38071a68-456a-4553-846a-fa67bf5596cc # User Light: no personal space
ROLE_CLIENT=38f0649f-b600-4188-b138-fbd2c773cc43   # oc-custom's Client: shared folders, language only
ROLE_EMPLOYEE=12047bc6-9aa3-49f6-8d78-e64b3065cd99 # oc-custom's Employee: shared folders, e-mail settings
ADMIN_TOKEN=""

usage() {
    cat <<'EOF'
Usage: ./users.sh COMMAND [USER] [OPTIONS]

  list                          OpenCloud's users, their role and sign-in state
  add USER [--name NAME] [--email ADDRESS] [--random]
                                [--role user|admin|light|client|employee]
                                Creates the user; asks for the password
                                (--random: makes one up and shows it)
  passwd USER [--random]        Sets a new password
  role USER ROLE                Changes the role: user, admin (2FA with Authelia),
                                light, client or employee. To light, client or
                                employee: disables the user's personal space
                                (files kept; shares and links from it stop);
                                back to user or admin: enables it again
  email USER ADDRESS            Changes the e-mail address
  disable USER, enable USER     Blocks or allows signing in
  delete USER                   Deletes the user (their files too)
  reset-2fa USER                Removes the user's 2FA devices (lost phone);
                                admins register a new one at the next sign-in

A password is read from stdin when it isn't a terminal (one line).
EOF
}

# --- OpenCloud's API, as admin with an app token, from inside its container

# token: makes this run's app token for admin. It's kept in memory only and
# expires after 10 minutes, so nothing usable is left behind. Called once
# before the command, not from api(): api() runs in subshells.
token() {
    local out i
    if [ -e .admin-token ]; then
        # users.sh before 7.2.4-7 kept a token here, valid for a year
        rm -f .admin-token
        echo "Removed .admin-token (an older users.sh's token). Its token stays valid until it expires: delete it under admin's Settings -> App Tokens." >&2
    fi
    for i in 1 2 3 4 5; do
        out="$(docker compose exec -T opencloud opencloud auth-app create --user-name admin --expiration 10m 2>&1)" && break
        sleep 5
    done
    ADMIN_TOKEN="$(printf '%s\n' "$out" | sed -n 's/^ *token: //p')"
    [ -n "$ADMIN_TOKEN" ] || die "Couldn't create an app token for admin: $out"
}

# cfg_quote: a value for curl's config file (in double quotes)
cfg_quote() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }

# api METHOD PATH [JSON [HEADER]]: the response body on stdout; fails on HTTP >= 300.
# The token and the body go through curl's config on stdin, not its command line.
api() {
    local method="$1" path="$2" body="${3-}" header="${4-}" out code
    [ -n "$ADMIN_TOKEN" ] || die "No app token (call token first)"
    out="$({
        printf 'user = "admin:%s"\n' "$(cfg_quote "$ADMIN_TOKEN")"
        [ -z "$body" ] || printf 'data-raw = "%s"\n' "$(cfg_quote "$body")"
        [ -z "$header" ] || printf 'header = "%s"\n' "$(cfg_quote "$header")"
    } | docker compose exec -T opencloud curl -sS -K - -X "$method" -H 'Content-Type: application/json' \
        -w '\n%{http_code}' "http://127.0.0.1:9200$path")"
    code="${out##*$'\n'}"
    out="${out%$'\n'*}"
    [ "$code" -lt 300 ] 2>/dev/null || die "OpenCloud: $method $path: HTTP $code $out"
    printf '%s' "$out"
}

json() { yq -p json -o json -I0 "$@"; } # JSON in, compact JSON out

user_id() { api GET "/graph/v1.0/users/$1" | yq -p json '.id'; }

set_role() { # USER ROLE_ID
    local id
    id="$(user_id "$1")"
    set_role_id "$id" "$2"
}

set_role_id() { # USER_ID ROLE_ID
    local app
    # Checked here: OpenCloud accepts an assignment with empty ids, and leaves the user without a role
    app="$(api GET /graph/v1.0/applications | yq -p json '.value[0].id // ""')" || app=""
    [ -n "$app" ] || die "OpenCloud: no application to assign the role from"
    export Y_ID="$1" Y_APP="$app" Y_ROLE="$2"
    api POST "/graph/v1.0/users/$id/appRoleAssignments" \
        "$(echo '{}' | json '.principalId = strenv(Y_ID) | .resourceId = strenv(Y_APP) | .appRoleId = strenv(Y_ROLE)')" > /dev/null
}

# personal_drive USER_ID: "DRIVE_ID STATE" of the user's personal space (STATE
# is trashed when it's disabled), nothing when they have none
personal_drive() {
    export Y_ID="$1"
    api GET '/graph/v1.0/drives?$filter=driveType%20eq%20%27personal%27' |
        yq -p json '.value[] | select(.owner.user.id == strenv(Y_ID)) | .id + " " + (.root.deleted.state // "")'
}

role_id() {
    case "$1" in
        admin) echo "$ROLE_ADMIN" ;;
        user) echo "$ROLE_USER" ;;
        light) echo "$ROLE_LIGHT" ;;
        client) echo "$ROLE_CLIENT" ;;
        employee) echo "$ROLE_EMPLOYEE" ;;
        *) die "Role: user, admin, light, client or employee" ;;
    esac
}

# --- Authelia's user file and database

users_yaml() { [ -f "$USERS_FILE" ] || die "No $USERS_FILE: run ./setup.sh"; cat "$USERS_FILE"; }

# edit_user USER EXPRESSION: applies a yq expression to .users[USER]
edit_user() {
    export Y_USER="$1"
    update_users ".users[strenv(Y_USER)] |= ($2)"
}

has_user() {
    export Y_USER="$1"
    [ "$(users_yaml | yq '.users | has(strenv(Y_USER))')" = true ]
}

authelia_cli() { # runs authelia's own CLI with this server's config
    docker compose exec -T -e AUTHELIA_NOTIFIER_FILESYSTEM_FILENAME=/tmp/cli-notifications.txt \
        authelia authelia --config /config/configuration.yml "$@"
}

# --- Passwords

read_password() { # [--random]: prints the password
    local p p2
    if [ "${1-}" = --random ]; then
        p="$(docker run --rm "$(conf AUTHELIA_IMAGE)" authelia crypto rand --length 16 --charset alphanumeric |
            sed -n 's/^Random Value: //p')"
        [ -n "$p" ] || die "Couldn't make up a password"
        echo "Password: $p" >&2
    elif [ -t 0 ]; then
        read -rsp "Password: " p; echo >&2
        read -rsp "Again: " p2; echo >&2
        [ "$p" = "$p2" ] || die "The passwords don't match"
    else
        IFS= read -r p || true
    fi
    [ -n "$p" ] || die "Empty password"
    [ "${#p}" -ge "$PASSWORD_MIN_LENGTH" ] || die "The password needs at least $PASSWORD_MIN_LENGTH characters"
    printf '%s' "$p"
}

valid_name() {
    [[ "$1" =~ ^[A-Za-z0-9_.@-]+$ ]] || die "Usernames may only contain letters, digits and _ . @ -"
}

# --- Commands

cmd_list() {
    local oc au
    oc="$(api GET '/graph/v1.0/users?$expand=appRoleAssignments' | yq -p json \
        '.value[] | [.onPremisesSamAccountName, .displayName, (.mail // "-"), .accountEnabled, (.appRoleAssignments[0].appRoleId // "-")] | map(tostring) | join("\t")')"
    if with_authelia; then
        au="$(users_yaml | yq '.users | to_entries | .[] | [.key, (.value.disabled // false), ((.value.groups // []) | join(",") | select(. != "") // "-")] | map(tostring) | join("\t")')"
    else
        au=""
    fi
    printf '%s\n' "$oc" | awk -F'\t' -v au="$au" -v a="$ROLE_ADMIN" -v u="$ROLE_USER" -v l="$ROLE_LIGHT" -v c="$ROLE_CLIENT" -v e="$ROLE_EMPLOYEE" -v with="$(with_authelia && echo 1)" '
        BEGIN {
            n = split(au, rows, "\n")
            for (i = 1; i <= n; i++) { split(rows[i], f, "\t"); if (f[1] != "") { off[f[1]] = f[2]; groups[f[1]] = f[3] } }
            role[a] = "admin"; role[u] = "user"; role[l] = "light"; role[c] = "client"; role[e] = "employee"
            printf "%-20s %-24s %-28s %-8s %-8s %s\n", "USER", "NAME", "E-MAIL", "ROLE", "ENABLED", (with ? "AUTHELIA" : "")
        }
        $1 != "" {
            s = ""
            if (with) s = !($1 in off) ? "missing" : (off[$1] == "true" ? "disabled" : "ok") (groups[$1] != "-" && $1 in off ? " (" groups[$1] ")" : "")
            printf "%-20s %-24s %-28s %-8s %-8s %s\n", $1, $2, $3, ($5 in role ? role[$5] : "-"), ($4 == "true" ? "yes" : "no"), s
        }'
}

cmd_add() {
    local user="${1-}" name="" email="" role=user random="" pw body hash rid id
    [ -n "$user" ] || die "add USER"; shift
    valid_name "$user"
    while [ $# -gt 0 ]; do
        case "$1" in
            --name) name="$2"; shift 2 ;;
            --email) email="$2"; shift 2 ;;
            --role) role="$2"; shift 2 ;;
            --random) random=--random; shift ;;
            *) die "Unknown option $1" ;;
        esac
    done
    rid="$(role_id "$role")"
    [ -n "$name" ] || name="$user"
    if with_authelia && has_user "$user"; then die "$user is already in $USERS_FILE"; fi
    pw="$(read_password $random)"
    if with_authelia; then
        # Authelia checks the password; OpenCloud's own gets a random one nobody uses
        hash="$(hash_password "$pw")"
        [ -n "$hash" ] || die "Couldn't hash the password"
        pw="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
    fi
    export Y_USER="$user" Y_NAME="$name" Y_EMAIL="$email" Y_HASH="$pw"
    body="$(echo '{}' | json '.onPremisesSamAccountName = strenv(Y_USER) | .displayName = strenv(Y_NAME)
        | .passwordProfile.password = strenv(Y_HASH) | .accountEnabled = false
        | (if strenv(Y_EMAIL) != "" then .mail = strenv(Y_EMAIL) else . end)')"
    # Created disabled and enabled once it has its role: until then it's a User
    # (personal space, sharing, links). If that fails, the account goes again.
    # The steps run in subshells, so a failing one returns instead of exiting.
    id="$(api POST /graph/v1.0/users "$body" | yq -p json '.id // ""')"
    [ -n "$id" ] || die "OpenCloud: created $user, but got no id back; check ./users.sh list"
    if ! { { [ "$role" = user ] || (set_role_id "$id" "$rid"); } &&
        (api PATCH "/graph/v1.0/users/$id" '{"accountEnabled":true}' > /dev/null); }; then
        (api DELETE "/graph/v1.0/users/$id" > /dev/null) ||
            die "Couldn't set up $user, nor remove the account again: ./users.sh delete $user"
        die "Couldn't set up $user; the account was removed again. Run add again."
    fi
    echo "OpenCloud: created $user"
    if with_authelia; then
        export Y_HASH="$hash" Y_GROUP="$([ "$role" = admin ] && echo admins || true)"
        update_users '.users[strenv(Y_USER)] = {"displayname": strenv(Y_NAME), "email": strenv(Y_EMAIL),
            "password": strenv(Y_HASH), "disabled": false,
            "groups": ([strenv(Y_GROUP)] | map(select(. != "")))}'
        echo "Authelia: added $user$([ "$role" = admin ] && echo ' (admins: 2FA required)' || true)"
    fi
}

cmd_passwd() {
    local user="${1-}" pw hash
    [ -n "$user" ] || die "passwd USER"
    valid_name "$user"
    pw="$(read_password "${2-}")"
    if with_authelia; then
        hash="$(hash_password "$pw")"
        [ -n "$hash" ] || die "Couldn't hash the password"
        export Y_HASH="$hash"
        if has_user "$user"; then
            edit_user "$user" '.password = strenv(Y_HASH)'
            echo "Authelia: new password for $user"
        else
            # In OpenCloud only (created in its web UI, or before Authelia):
            # add them to Authelia with OpenCloud's name, e-mail and role
            local u
            u="$(api GET "/graph/v1.0/users/$user?\$expand=appRoleAssignments")"
            export Y_USER="$user" Y_NAME="$(yq -p json '.displayName // ""' <<< "$u")" \
                Y_EMAIL="$(yq -p json '.mail // ""' <<< "$u")" \
                Y_GROUP="$([ "$(yq -p json '.appRoleAssignments[0].appRoleId // ""' <<< "$u")" = "$ROLE_ADMIN" ] && echo admins || true)"
            update_users '.users[strenv(Y_USER)] = {"displayname": strenv(Y_NAME), "email": strenv(Y_EMAIL),
                "password": strenv(Y_HASH), "disabled": false,
                "groups": ([strenv(Y_GROUP)] | map(select(. != "")))}'
            echo "Authelia: added $user with a password$([ -n "$Y_GROUP" ] && echo ' (admins: 2FA required)' || true)"
        fi
    else
        export Y_HASH="$pw"
        api PATCH "/graph/v1.0/users/$(user_id "$user")" \
            "$(echo '{}' | json '.passwordProfile.password = strenv(Y_HASH)')" > /dev/null
        echo "OpenCloud: new password for $user"
    fi
}

cmd_role() {
    local user="${1-}" role="${2-}" rid id drive="" state="" with_space answer
    [ -n "$user" ] && [ -n "$role" ] || die "role USER user|admin|light|client|employee"
    valid_name "$user"
    rid="$(role_id "$role")"
    id="$(user_id "$user")"
    read -r drive state <<< "$(personal_drive "$id")" || true
    # These types have no personal space. One the user already has is disabled,
    # not deleted: the files stay and an admin can enable it again (the owner
    # can't, patches/reva/0002), and shares and links from it stop working.
    case "$role" in client|employee|light) with_space="" ;; *) with_space=1 ;; esac
    if [ -z "$with_space" ] && [ -n "$drive" ] && [ "$state" != trashed ] && [ -t 0 ]; then
        read -rp "$user has a personal space. As $role they don't: it gets disabled (files kept, an admin can enable it again), and their shares and links from it stop working. Continue? [y/N] " answer
        case "$answer" in y|Y) ;; *) die "Nothing changed" ;; esac
    fi
    set_role_id "$id" "$rid"
    echo "OpenCloud: $user is now $role"
    if [ -z "$with_space" ] && [ -n "$drive" ] && [ "$state" != trashed ]; then
        (api DELETE "/graph/v1.0/drives/${drive//\$/%24}" > /dev/null) ||
            die "$user is $role now, but their personal space is still enabled: run the same command again"
        echo "OpenCloud: disabled $user's personal space (their shares and links from it stop working)"
    elif [ -n "$with_space" ] && [ "$state" = trashed ]; then
        (api PATCH "/graph/v1.0/drives/${drive//\$/%24}" '{}' 'Restore: T' > /dev/null) ||
            die "$user is $role now, but their personal space is still disabled: run the same command again"
        echo "OpenCloud: enabled $user's personal space again"
    fi
    if with_authelia && has_user "$user"; then
        if [ "$role" = admin ]; then
            edit_user "$user" '.groups = ((.groups // []) + ["admins"] | unique)'
            echo "Authelia: $user is in admins (2FA required)"
        else
            edit_user "$user" '.groups = ((.groups // []) - ["admins"])'
        fi
    fi
}

cmd_email() {
    local user="${1-}" email="${2-}"
    [ -n "$user" ] && [ -n "$email" ] || die "email USER ADDRESS"
    valid_name "$user"
    export Y_EMAIL="$email"
    api PATCH "/graph/v1.0/users/$(user_id "$user")" "$(echo '{}' | json '.mail = strenv(Y_EMAIL)')" > /dev/null
    if with_authelia && has_user "$user"; then edit_user "$user" '.email = strenv(Y_EMAIL)'; fi
    echo "$user: e-mail is now $email"
}

cmd_enable() { # USER true|false
    local user="${1-}" on="$2"
    [ -n "$user" ] || die "USER missing"
    valid_name "$user"
    export Y_FLAG="$on"
    api PATCH "/graph/v1.0/users/$(user_id "$user")" "$(echo '{}' | json '.accountEnabled = (strenv(Y_FLAG) == "true")')" > /dev/null
    if with_authelia && has_user "$user"; then edit_user "$user" '.disabled = (strenv(Y_FLAG) != "true")'; fi
    echo "$user $([ "$on" = true ] && echo enabled || echo disabled)"
}

cmd_delete() {
    local user="${1-}" answer
    [ -n "$user" ] || die "delete USER"
    valid_name "$user"
    [ "$user" != admin ] || die "Not deleting admin"
    if [ -t 0 ]; then
        read -rp "Delete $user and their files? Type the username: " answer
        [ "$answer" = "$user" ] || die "Not deleted"
    fi
    api DELETE "/graph/v1.0/users/$(user_id "$user")" > /dev/null
    echo "OpenCloud: deleted $user"
    if with_authelia && has_user "$user"; then
        update_users 'del(.users[strenv(Y_USER)])'
        (reset_2fa "$user") > /dev/null 2>&1 || true
        echo "Authelia: removed $user"
    fi
}

reset_2fa() { # prints what was removed
    local out
    if out="$(authelia_cli storage user totp delete "$1" 2>&1)"; then
        echo "Authelia: removed $1's TOTP app"
    elif ! grep -q 'no TOTP configuration' <<< "$out"; then
        die "Authelia: $(head -1 <<< "$out")"
    fi
    out="$(authelia_cli storage user webauthn delete "$1" --all 2>&1)" || die "Authelia: $(head -1 <<< "$out")"
}

cmd_reset_2fa() {
    local user="${1-}"
    [ -n "$user" ] || die "reset-2fa USER"
    with_authelia || die "No Authelia on this server (COMPOSE_FILE in .env)"
    valid_name "$user"
    reset_2fa "$user"
    echo "Authelia: $user has no 2FA devices now"
}

cmd="${1-help}"; shift || true
case "$cmd" in
    list|add|passwd|role|email|disable|enable|delete) token ;;
esac
case "$cmd" in
    list) cmd_list ;;
    add) cmd_add "$@" ;;
    passwd) cmd_passwd "$@" ;;
    role) cmd_role "$@" ;;
    email) cmd_email "$@" ;;
    disable) cmd_enable "${1-}" false ;;
    enable) cmd_enable "${1-}" true ;;
    delete) cmd_delete "${1-}" ;;
    reset-2fa) cmd_reset_2fa "${1-}" ;;
    help|-h|--help) usage ;;
    *) usage >&2; exit 1 ;;
esac
