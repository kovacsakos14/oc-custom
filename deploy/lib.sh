# Shared by setup.sh and users.sh (sourced, run from this folder).

YQ_IMAGE=mikefarah/yq:4.54.1@sha256:4b3d9475d65571d28cbb19544d3820ec2945e4c8b2f18279394282b8dc3a592e
USERS_FILE=authelia/users/users_database.yml
# The shortest password users.sh and setup.sh accept; Authelia's own pages
# check password_policy in authelia/configuration.yml, keep both the same
PASSWORD_MIN_LENGTH=3

die() { echo "$*" >&2; exit 1; }

[ -f .env ] || die "No .env here: cp .env.example .env, fill it in, then run ./setup.sh"

# conf KEY: KEY's value in .env, read as docker compose reads it: the last
# line wins, "export " is allowed, a comment after an unquoted value starts at
# " #", quoted values keep their # and lose their quotes (\" and \\ unescaped
# in double quotes). ${...} isn't expanded: a value with it is refused, so
# these scripts never use a different value than compose would.
conf() {
    local v
    v="$(awk -v k="$1" '
        { sub(/^[ \t]*export[ \t]+/, "") }
        index($0, k "=") == 1 {
            v = substr($0, length(k) + 2)
            if (v ~ /^"/) {
                v = substr(v, 2); sub(/"[ \t]*(#.*)?$/, "", v)
                gsub(/\\"/, "\"", v); gsub(/\\\\/, "\\", v)
            } else if (v ~ /^'\''/) {
                v = substr(v, 2); sub(/'\''[ \t]*(#.*)?$/, "", v)
            } else {
                sub(/[ \t]+#.*$/, "", v); sub(/[ \t]+$/, "", v)
            }
        }
        END { print v }' .env)"
    case "$v" in *'${'*) die "$1 in .env: setup.sh and users.sh don't expand \${...}; write the value out" ;; esac
    printf '%s\n' "$v"
}

with_authelia() { case "$(conf COMPOSE_FILE)" in *compose.authelia.yaml*) return 0 ;; esac; return 1; }

# yq on stdin; values go in as environment variables (strenv(NAME) in the
# expression), never into the expression itself
yq() {
    envs=""
    for v in Y_USER Y_NAME Y_EMAIL Y_HASH Y_GROUP Y_FLAG Y_ID Y_APP Y_ROLE Y_TOKEN; do envs="$envs -e $v"; done
    # shellcheck disable=SC2086 # word splitting is the point
    docker run --rm -i $envs "$YQ_IMAGE" "$@"
}

# hash_password PASSWORD: an argon2id hash from Authelia's own tool. The password goes
# through stdin; it's in the throwaway container's command line briefly.
hash_password() {
    printf '%s' "$1" | docker run --rm -i "$(conf AUTHELIA_IMAGE)" \
        sh -c 'authelia crypto hash generate argon2 --password "$(cat)"' | sed -n 's/^Digest: //p'
}

USERS_LOCK=authelia/.users.lock
USERS_TMP=""

# Commands run at exit, also after die: EXIT_HOOKS+=('command')
EXIT_HOOKS=('[ -z "$USERS_TMP" ] || rm -f "$USERS_TMP"')
on_exit() { local h; for h in "${EXIT_HOOKS[@]}"; do eval "$h" || true; done; }
trap on_exit EXIT

# lock_users: one users.sh at a time changes the user file, until it exits
# (the lock lives outside users/, the folder Authelia watches)
lock_users() {
    [ -z "${USERS_LOCKED-}" ] || return 0
    if command -v flock > /dev/null; then
        exec 9> "$USERS_LOCK"
        flock -w 60 9 || die "Another users.sh is changing $USERS_FILE; try again"
    else
        echo "Warning: no flock on this host, so two users.sh at the same time could undo each other's changes" >&2
    fi
    USERS_LOCKED=1
}

users_sum() { cksum < "$USERS_FILE" 2> /dev/null || echo none; }

# update_users EXPRESSION: applies a yq expression to Authelia's user file
# (users: {} when there's none yet). The result is checked and written whole,
# then renamed into place, because Authelia stops on a broken file. Authelia
# itself rewrites the file when a user changes their password; if that
# happens meanwhile, the change is made again on top of it.
update_users() {
    local base
    lock_users
    USERS_TMP="$USERS_FILE.new.$$"
    for _ in 1 2 3; do
        base="$(users_sum)"
        # umask: the hashes are never readable by others, not even briefly
        ( umask 077
          { if [ -f "$USERS_FILE" ]; then cat "$USERS_FILE"; else printf 'users: {}\n'; fi; } | yq "$1" > "$USERS_TMP" )
        yq -e '(.users | type == "!!map") and ([.users[] | select(.password == null or .displayname == null)] | length == 0)' < "$USERS_TMP" > /dev/null ||
            die "Refusing to write $USERS_FILE: the result isn't a valid user list"
        if [ "$(users_sum)" = "$base" ]; then
            chmod 600 "$USERS_TMP"
            mv "$USERS_TMP" "$USERS_FILE"
            USERS_TMP=""
            return 0
        fi
        echo "$USERS_FILE changed meanwhile (Authelia?); making the change again" >&2
    done
    die "$USERS_FILE keeps changing; nothing written, run again"
}
