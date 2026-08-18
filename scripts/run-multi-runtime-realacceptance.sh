#!/usr/bin/env bash
set -euo pipefail

required_opt_ins=(
  LIEXIU_RUN_REAL_ACCEPTANCE
  LIEXIU_ALLOW_RUNTIME_SUBPROCESSES
  LIEXIU_ALLOW_EXTERNAL_QUOTA
  LIEXIU_ALLOW_REAL_ACCEPTANCE_CLEANUP
)
for name in "${required_opt_ins[@]}"; do
  if [ "${!name:-}" != "1" ]; then
    echo "real acceptance disabled: set $name=1 explicitly" >&2
    exit 2
  fi
done

required_values=(
  LIEXIU_REAL_ACCEPTANCE_DATABASE_URL
  LIEXIU_REAL_PROVIDER_A
  LIEXIU_REAL_PROVIDER_A_PATH_ENV
  LIEXIU_REAL_PROVIDER_A_EXECUTABLE
  LIEXIU_REAL_PROVIDER_A_MODEL
  LIEXIU_REAL_PROVIDER_B
  LIEXIU_REAL_PROVIDER_B_PATH_ENV
  LIEXIU_REAL_PROVIDER_B_EXECUTABLE
  LIEXIU_REAL_PROVIDER_B_MODEL
  LIEXIU_REAL_ACCEPTANCE_TIMEOUT
  LIEXIU_REAL_ROLE_TIMEOUT_SECONDS
  LIEXIU_REAL_MAX_TOKENS
  LIEXIU_REAL_MAX_COST_USD_TICKS
)
for name in "${required_values[@]}"; do
  if [ -z "${!name:-}" ]; then
    echo "real acceptance configuration missing: $name" >&2
    exit 2
  fi
  case "${!name}" in
    *$'\n'*|*$'\r'*)
      echo "real acceptance configuration must be single-line: $name" >&2
      exit 2
      ;;
  esac
done

database_without_query=${LIEXIU_REAL_ACCEPTANCE_DATABASE_URL%%\?*}
case "$database_without_query" in
  postgres://*/*|postgresql://*/*) ;;
  *)
    echo "LIEXIU_REAL_ACCEPTANCE_DATABASE_URL must be an absolute PostgreSQL URL" >&2
    exit 2
    ;;
esac
database_authority_and_path=${database_without_query#*://}
database_name=${database_authority_and_path#*/}
case "$database_name" in
  ''|*/*)
    echo "real acceptance database URL must identify exactly one database path segment" >&2
    exit 2
    ;;
esac
case "$database_name" in
  liexiu_realacceptance_*|liexiu_wave4b6_acceptance_*) ;;
  *)
    echo "real acceptance requires a dedicated database whose name starts with liexiu_realacceptance_ or liexiu_wave4b6_acceptance_" >&2
    exit 2
    ;;
esac

if [ "$LIEXIU_REAL_PROVIDER_A" = "$LIEXIU_REAL_PROVIDER_B" ]; then
  echo "real acceptance requires two distinct provider identities" >&2
  exit 2
fi

for name in LIEXIU_REAL_PROVIDER_A_PATH_ENV LIEXIU_REAL_PROVIDER_B_PATH_ENV; do
  if [[ ! ${!name} =~ ^[A-Z_][A-Z0-9_]*$ ]]; then
    echo "$name must be an uppercase environment variable name" >&2
    exit 2
  fi
done
if [ "$LIEXIU_REAL_PROVIDER_A_PATH_ENV" = "$LIEXIU_REAL_PROVIDER_B_PATH_ENV" ]; then
  echo "real acceptance requires two distinct provider path environment names" >&2
  exit 2
fi

validate_credential_envs() {
  config_name=$1
  config_value=${!config_name:-}
  [ -z "$config_value" ] && return 0
  IFS=',' read -r -a credential_names <<< "$config_value"
  if [ "${#credential_names[@]}" -gt 8 ]; then
    echo "$config_name may name at most eight provider credential environments" >&2
    exit 2
  fi
  for credential_name in "${credential_names[@]}"; do
    credential_name=${credential_name//[[:space:]]/}
    if [[ ! $credential_name =~ ^[A-Z_][A-Z0-9_]*$ ]]; then
      echo "$config_name contains an invalid provider credential environment name" >&2
      exit 2
    fi
    case "$credential_name" in
      LIEXIU_REAL_*|DATABASE_URL|PG*|POSTGRES_*|REDIS_URL|JWT_SECRET|LIEXIU_TOKEN|LIEXIU_OWNER_BOOTSTRAP_SECRET|LIEXIU_VCS_SECRET_KEY|LIEXIU_LLM_API_KEY)
        echo "$config_name cannot allow a server or acceptance secret" >&2
        exit 2
        ;;
      *KEY*|*TOKEN*|*SECRET*|*AUTH*|*PASSWORD*|*CREDENTIAL*) ;;
      *)
        echo "$config_name may allow only credential-like environment names" >&2
        exit 2
        ;;
    esac
    if [ -z "${!credential_name:-}" ]; then
      echo "$config_name names a missing provider credential environment" >&2
      exit 2
    fi
    case "${!credential_name}" in
      *$'\n'*|*$'\r'*)
        echo "$config_name requires single-line provider credential values" >&2
        exit 2
        ;;
    esac
  done
}
validate_credential_envs LIEXIU_REAL_PROVIDER_A_CREDENTIAL_ENVS
validate_credential_envs LIEXIU_REAL_PROVIDER_B_CREDENTIAL_ENVS

normalized_provider_path_env() {
  normalized=$(printf '%s' "$1" | tr '[:lower:]' '[:upper:]' | sed 's/[^A-Z0-9]/_/g')
  printf '%s_PATH' "$normalized"
}
provider_a_path_env=$(normalized_provider_path_env "$LIEXIU_REAL_PROVIDER_A")
provider_b_path_env=$(normalized_provider_path_env "$LIEXIU_REAL_PROVIDER_B")
case "$LIEXIU_REAL_PROVIDER_A_PATH_ENV" in
  "LIEXIU_$provider_a_path_env") ;;
  *) echo "LIEXIU_REAL_PROVIDER_A_PATH_ENV must match provider A identity" >&2; exit 2 ;;
esac
case "$LIEXIU_REAL_PROVIDER_B_PATH_ENV" in
  "LIEXIU_$provider_b_path_env") ;;
  *) echo "LIEXIU_REAL_PROVIDER_B_PATH_ENV must match provider B identity" >&2; exit 2 ;;
esac

for name in LIEXIU_REAL_PROVIDER_A_EXECUTABLE LIEXIU_REAL_PROVIDER_B_EXECUTABLE; do
  executable=${!name}
  case "$executable" in
    /*) ;;
    *)
      echo "$name must be an absolute path" >&2
      exit 2
      ;;
  esac
  if [ ! -f "$executable" ] || [ ! -x "$executable" ]; then
    echo "$name must identify an executable regular file" >&2
    exit 2
  fi
done

for name in LIEXIU_REAL_ROLE_TIMEOUT_SECONDS LIEXIU_REAL_MAX_TOKENS LIEXIU_REAL_MAX_COST_USD_TICKS; do
  value=${!name}
  if [[ ! $value =~ ^[0-9]+$ ]] || [ -z "${value//0/}" ]; then
    echo "$name must be a positive integer" >&2
    exit 2
  fi
done
if [ "$LIEXIU_REAL_MAX_TOKENS" -lt 3 ] || [ "$LIEXIU_REAL_MAX_COST_USD_TICKS" -lt 3 ]; then
  echo "LIEXIU_REAL_MAX_TOKENS and LIEXIU_REAL_MAX_COST_USD_TICKS must each be at least 3" >&2
  exit 2
fi
if [[ ! $LIEXIU_REAL_ACCEPTANCE_TIMEOUT =~ ^([0-9]+([.][0-9]+)?(ns|us|µs|ms|s|m|h))+$ ]] || [[ $LIEXIU_REAL_ACCEPTANCE_TIMEOUT =~ ^0+([.]0+)?(ns|us|µs|ms|s|m|h)$ ]]; then
  echo "LIEXIU_REAL_ACCEPTANCE_TIMEOUT must be a positive Go duration" >&2
  exit 2
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
acceptance_build_root=$(mktemp -d "${TMPDIR:-/tmp}/liexiu-realacceptance.XXXXXX")
active_pid=
cleanup_build_root() {
  case "$acceptance_build_root" in
    "${TMPDIR:-/tmp}"/liexiu-realacceptance.*) rm -rf -- "$acceptance_build_root" ;;
    *) echo "refusing to remove unexpected acceptance build root" >&2 ;;
  esac
}
terminate_active_child() {
  trap - INT TERM
  if [ -n "$active_pid" ]; then
    kill -TERM "$active_pid" 2>/dev/null || true
    wait "$active_pid" 2>/dev/null || true
    active_pid=
  fi
  exit 130
}
run_child() {
  "$@" &
  active_pid=$!
  if wait "$active_pid"; then
    child_rc=0
  else
    child_rc=$?
  fi
  active_pid=
  return "$child_rc"
}
trap cleanup_build_root EXIT
trap terminate_active_child INT TERM

version=$(git -C "$repo_root" describe --tags --match 'v[0-9]*' --always --dirty 2>/dev/null || echo dev)
commit=$(git -C "$repo_root" rev-parse --short HEAD 2>/dev/null || echo unknown)
date_utc=$(date -u '+%Y-%m-%dT%H:%M:%SZ')

echo "==> Building the isolated real-acceptance daemon binary"
run_child go -C "$repo_root/server" build \
  -ldflags "-X main.version=$version -X main.commit=$commit -X main.date=$date_utc" \
  -o "$acceptance_build_root/liexiu" ./cmd/liexiu

echo "==> Building the isolated tagged acceptance test binary"
run_child go -C "$repo_root/server" test -c -tags=realacceptance \
	-ldflags "-X github.com/kailonyang/liexiu/server/cmd/server.version=$version" \
	-o "$acceptance_build_root/multi-runtime-realacceptance.test" ./cmd/server

echo "==> Running the explicitly authorized two-runtime Mission acceptance"
unset DATABASE_URL
run_child env LIEXIU_REAL_DAEMON_BINARY="$acceptance_build_root/liexiu" \
  "$acceptance_build_root/multi-runtime-realacceptance.test" \
    -test.run '^TestMultiRuntimeRealAcceptance$' -test.count=1 -test.v
