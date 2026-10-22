# Shared by setup.sh and users.sh (sourced, run from this folder).

YQ_IMAGE=mikefarah/yq:4.54.1@sha256:4b3d9475d65571d28cbb19544d3820ec2945e4c8b2f18279394282b8dc3a592e
USERS_FILE=authelia/users/users_database.yml

die() { echo "$*" >&2; exit 1; }

[ -f .env ] || die "No .env here: cp .env.example .env, fill it in, then run ./setup.sh"

# conf KEY: KEY's value in .env (the last one wins; outer quotes removed)
conf() {
    awk -v k="$1" 'index($0, k "=") == 1 { v = substr($0, length(k) + 2) }
        END { if (v ~ /^".*"$/ || v ~ /^'\''.*'\''$/) v = substr(v, 2, length(v) - 2); print v }' .env
}

with_authelia() { case "$(conf COMPOSE_FILE)" in *compose.authelia.yaml*) return 0 ;; esac; return 1; }

# yq on stdin; values go in as environment variables (strenv(NAME) in the
# expression), never into the expression itself
yq() {
    envs=""
    for v in Y_USER Y_NAME Y_EMAIL Y_HASH Y_GROUP Y_FLAG Y_ID Y_APP Y_ROLE; do envs="$envs -e $v"; done
    # shellcheck disable=SC2086 # word splitting is the point
    docker run --rm -i $envs "$YQ_IMAGE" "$@"
}

# hash_password PASSWORD: an argon2id hash from Authelia's own tool. The password goes
# through stdin; it's in the throwaway container's command line briefly.
hash_password() {
    printf '%s' "$1" | docker run --rm -i "$(conf AUTHELIA_IMAGE)" \
        sh -c 'authelia crypto hash generate argon2 --password "$(cat)"' | sed -n 's/^Digest: //p'
}

# write_users: stdin becomes Authelia's user file, after a check; written
# whole and renamed into place, because Authelia stops on a broken file
write_users() {
    tmp="$USERS_FILE.new.$$"
    cat > "$tmp"
    if ! yq -e '(.users | type == "!!map") and ([.users[] | select(.password == null or .displayname == null)] | length == 0)' < "$tmp" > /dev/null; then
        rm -f "$tmp"
        die "Refusing to write $USERS_FILE: the result isn't a valid user list"
    fi
    chmod 600 "$tmp"
    mv "$tmp" "$USERS_FILE"
}
