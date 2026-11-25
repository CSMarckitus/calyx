#!/usr/bin/env bash
# Runs the Python tools in Docker. The Python tools are never installed on the host.
# Usage: run.sh test [pytest arguments] | test-core [pytest arguments] | test-packaging
#   | types | lint | format | readme-check | wheel-check | protoc | protoc-check
#   | lock [uv arguments]
set -euo pipefail

# The docker command can exist while its daemon does not run.
if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
  echo "Docker is necessary for the Python tools." >&2
  exit 1
fi

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
python_dir=$(dirname "$here")
repo=$(cd "$python_dir/../../.." && pwd)

# Build once: Docker reuses the cached layers on later runs.
build() {
  docker build --quiet --target "$1" --tag "calyx-$1" "$here" >/dev/null
}

# run <target> <workdir> <command...>
# HOME, the caches and the installed wheel are in /tmp of the container, so no run leaves files in
# the repository.
run() {
  local target=$1 workdir=$2
  shift 2
  build "$target"
  docker run --rm \
    --user "$(id -u):$(id -g)" \
    --volume "$repo:/repo:ro" \
    --volume "$python_dir:/repo/packages/client-sdk/python" \
    --env HOME=/tmp \
    --env PYTHONDONTWRITEBYTECODE=1 \
    --env PIP_DISABLE_PIP_VERSION_CHECK=1 \
    --env MYPY_CACHE_DIR=/tmp/mypy \
    --env RUFF_CACHE_DIR=/tmp/ruff \
    --env PYTEST_ADDOPTS="-o cache_dir=/tmp/pytest" \
    --workdir "/repo/packages/client-sdk/python/$workdir" \
    "calyx-$target" "$@"
}

# with_wheels <target> <workdir> <script> [args...]: builds both wheels, then runs the script.
with_wheels() {
  local target=$1 workdir=$2 script=$3
  shift 3
  run "$target" "$workdir" sh -ec "
    (cd ../backend && hatchling build -t wheel -d /tmp/dist)
    (cd ../core && hatchling build -t wheel -d /tmp/dist)
    $script" sh "$@"
}

# The image has the dependencies. --no-index makes pip fail when the lock file lacks one. The wheels
# are not on PyPI, so --find-links gives pip their folder.
install='pip install --quiet --user --no-index --find-links /tmp/dist'

# This test downloads packages, so it takes more than the 10 s of another test.
test_packaging() {
  with_wheels python-3.12 core "$install calyx-client-sdk-core
    pytest -s -o faulthandler_timeout=300 tests/test_packaging.py"
}

# protoc_into <folder>: writes the protobuf code into a folder of the container. The old files go
# first, so the module of a removed .proto file does not stay. There are no _pb2_grpc.py files.
protoc_into='
  protoc_into() {
    rm -rf "$1"
    mkdir "$1"
    python -m grpc_tools.protoc --proto_path=/repo/packages/shared-types/proto \
      --python_out="$1" --pyi_out="$1" /repo/packages/shared-types/proto/calyx/core/v1/*.proto
    find "$1" -type d -exec touch {}/__init__.py ";"
  }
'
proto=src/calyx_client_sdk_core/_proto

task=${1:-}
case "$task" in
test)
  # Extra arguments go to pytest, for example: run.sh test -k version
  with_wheels python-3.12 backend "$install calyx-client-sdk; pytest \"\$@\"" "${@:2}"
  with_wheels python-3.14 backend "$install calyx-client-sdk; pytest \"\$@\"" "${@:2}"
  ;;
test-core)
  core_tests="$install calyx-client-sdk-core; pytest --ignore=tests/test_packaging.py \"\$@\""
  with_wheels python-3.12 core "$core_tests" "${@:2}"
  with_wheels python-3.14 core "$core_tests" "${@:2}"
  # With pytest arguments, the call is for some tests only.
  if [ $# -eq 1 ]; then test_packaging; fi
  ;;
test-packaging)
  test_packaging
  ;;
types)
  with_wheels python-3.12 backend "$install calyx-client-sdk"'
    mypy --strict --package calyx_client_sdk
    pyright "$(python -m site --user-site)/calyx_client_sdk"
    python tests/types/check.py tests/types
    # In backend/, pyright reads src/ and not the installed wheel. So the tools use a copy.
    cp -r examples /tmp/examples
    cp pyproject.toml /tmp/examples
    cd /tmp/examples
    # Celery and Django have no types. mypy can accept that, but pyright cannot check these two.
    mypy --strict --ignore-missing-imports --allow-untyped-decorators --allow-subclassing-any \
      celery_task.py django_webhook.py
    rm celery_task.py django_webhook.py
    mypy --strict .
    pyright .
  '
  with_wheels python-3.12 core "$install calyx-client-sdk-core"'
    mypy --strict --package calyx_client_sdk_core
    python ../backend/tests/types/check.py tests/types
    cp -r examples /tmp/examples
    cp pyproject.toml /tmp/examples
    (cd /tmp/examples && mypy --strict . && pyright .)
    # The folders that pyright ignores are below the folder of pyproject.toml.
    site=$(python -m site --user-site)
    cp pyproject.toml "$site"
    cd "$site"
    pyright calyx_client_sdk_core
  '
  ;;
lint)
  run python-3.12 . ruff check backend core
  run python-3.12 . ruff format --check backend core
  ;;
format)
  run python-3.12 . ruff check --fix backend core
  run python-3.12 . ruff format backend core
  ;;
readme-check)
  run python-3.12 backend python tests/readme_check.py .
  run python-3.12 backend python tests/readme_check.py ../core
  ;;
wheel-check)
  # Each clean environment has only the wheels and their dependencies, with the versions of the lock
  # file. The paths of the wheels make sure that no package of PyPI replaces one.
  with_wheels python-3.12 backend '
    # check <wheels> <package> <wrong names>: installs, then looks for wrong top-level names.
    check() {
      uv venv --quiet --clear /tmp/clean
      uv pip install --quiet --python /tmp/clean --constraint ../docker/requirements-dev.txt $1
      for name in calyx calyx.py examples tests $3; do
        if [ -e "/tmp/clean/lib/python3.12/site-packages/$name" ]; then
          echo "wheel-check failed: the installation of $2 has a top-level $name." >&2
          exit 1
        fi
      done
      if [ ! -f "/tmp/clean/lib/python3.12/site-packages/$2/py.typed" ]; then
        echo "wheel-check failed: the wheel of $2 has no py.typed." >&2
        exit 1
      fi
      /tmp/clean/bin/python -I -c "import $2"
    }
    # google is the folder of protobuf.
    check "$(echo /tmp/dist/calyx_client_sdk-*.whl)" calyx_client_sdk "grpc google"
    check "$(echo /tmp/dist/*.whl)" calyx_client_sdk_core ""
    /tmp/clean/bin/python -I -c "
from importlib.metadata import requires, version
from calyx_client_sdk_core._proto.calyx.core.v1 import event_pb2, instance_pb2, message_pb2
backend, core = version(\"calyx-client-sdk\"), version(\"calyx-client-sdk-core\")
if backend != core or f\"calyx-client-sdk=={core}\" not in requires(\"calyx-client-sdk-core\"):
    raise SystemExit(\"wheel-check failed: the core package must require exactly its own version of the backend.\")
"
  '
  ;;
protoc)
  run python-protoc core sh -ec "$protoc_into protoc_into $proto"
  ;;
protoc-check)
  # The same command into a temporary folder. A difference means that _proto is old.
  run python-protoc core sh -ec "$protoc_into"'
    protoc_into /tmp/proto
    diff -r /tmp/proto "$1" || {
      echo "protoc-check failed: run docker/run.sh protoc." >&2
      exit 1
    }
  ' sh "$proto"
  ;;
lock)
  # uv keeps the versions of the old lock files. To get newer versions: run.sh lock --upgrade
  # calyx-client-sdk is not on PyPI, so uv must not look for it there.
  run python-3.12 . sh -ec '
    compile() {
      output=$1
      shift
      uv pip compile --quiet --universal --python-version 3.12 --generate-hashes \
        --custom-compile-command "docker/run.sh lock" --output-file "$output" "$@"
    }
    echo calyx-client-sdk >/tmp/excludes
    compile docker/requirements-dev.txt "$@" --excludes /tmp/excludes \
      docker/requirements-dev.in backend/pyproject.toml core/pyproject.toml
    compile docker/requirements-protoc.txt "$@" docker/requirements-protoc.in
  ' sh "${@:2}"
  ;;
*)
  echo "Usage: run.sh test [pytest arguments] | test-core [pytest arguments] | test-packaging | types | lint | format | readme-check | wheel-check | protoc | protoc-check | lock [uv arguments]" >&2
  exit 2
  ;;
esac
