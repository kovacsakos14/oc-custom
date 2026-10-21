# Shared by the test helpers: reads test/local.env (see local.env.example).
# shellcheck shell=bash
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [ -f "$repo/test/local.env" ]; then
    # shellcheck disable=SC1091
    source "$repo/test/local.env"
fi
OC_URL="${OC_URL:-https://localhost:9200}"
need() { # names of settings that must be set
    for v in "$@"; do
        [ -n "${!v:-}" ] || { echo "Set $v in test/local.env (see test/local.env.example)" >&2; exit 2; }
    done
}
