#!/usr/bin/env bash
# Runs the PHP tools in Docker. The PHP tools are never installed on the host.
# Usage: run.sh composer | composer-core | test-core | lint-core | test-client | lint-client | deps-composer | archive-check | protoc | protoc-check
set -euo pipefail

# The docker command can exist while its daemon does not run.
if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
  echo "Docker is necessary for the PHP tests." >&2
  exit 1
fi

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
php_dir=$(dirname "$here")
repo=$(cd "$php_dir/../../.." && pwd)

# Build once: Docker reuses the cached layers on later runs.
build() {
  docker build --quiet --target "$1" --tag "calyx-php-$1" "$here" >/dev/null
}

# run <target> <workdir> <command...>
run() {
  local target=$1 workdir=$2
  shift 2
  build "$target"
  docker run --rm \
    --user "$(id -u):$(id -g)" \
    --volume "$repo:/repo:ro" \
    --volume "$php_dir:/repo/packages/client-sdk/php" \
    --env HOME=/tmp \
    --env COMPOSER_HOME=/tmp/composer \
    --workdir "/repo/packages/client-sdk/php/$workdir" \
    "calyx-php-$target" "$@"
}

# phpunit <target> [args...]: runs PHPUnit with the php -S server of the curl tests in the same container.
# The EXIT trap stops the server also when PHPUnit fails.
phpunit() {
  local target=$1
  shift
  run "$target" client sh -c '
    PHP_CLI_SERVER_WORKERS=4 php -S 127.0.0.1:8765 tests/Fake/router.php >/tmp/server.log 2>&1 &
    server=$!
    trap "kill $server 2>/dev/null" EXIT
    for _ in $(seq 50); do
      php -r "exit(@fsockopen(\"127.0.0.1\", 8765) ? 0 : 1);" && break
      sleep 0.1
    done
    CALYX_TEST_SERVER=127.0.0.1:8765 vendor/bin/phpunit "$@"
  ' sh "$@"
}

task=${1:-}
case "$task" in
composer)
  run backend-8.3 client composer update --no-interaction --no-progress
  ;;
composer-core)
  run core core composer update --no-interaction --no-progress
  ;;
protoc)
  # The old classes go first, so a class of a removed message does not stay.
  run core core sh -ec '
    rm -rf generated
    mkdir generated
    protoc --proto_path=/repo/packages/shared-types/proto --proto_path=/usr/include \
      --php_out=/repo/packages/client-sdk/php/core/generated /repo/packages/shared-types/proto/calyx/core/v1/*.proto
  '
  ;;
protoc-check)
  # The same command into a temporary folder. A difference means that generated/ is old.
  run core core sh -ec '
    mkdir /tmp/generated
    protoc --proto_path=/repo/packages/shared-types/proto --proto_path=/usr/include \
      --php_out=/tmp/generated /repo/packages/shared-types/proto/calyx/core/v1/*.proto
    diff -r /tmp/generated generated || {
      echo "protoc-check failed: run docker/run.sh protoc." >&2
      exit 1
    }
  '
  ;;
test-core)
  # The host builds fakecore. The container sees it through the mount of the php folder.
  (cd "$php_dir/fakecore" && CGO_ENABLED=0 GOPRIVATE='github.com/csmarckitus/*' go build -o fakecore .)
  run core core composer install --no-interaction --no-progress
  # The EXIT trap stops fakecore also when PHPUnit fails. Extra arguments go to PHPUnit.
  run core core sh -c '
    ../fakecore/fakecore -addr 127.0.0.1:50099 -proxy-addr 127.0.0.1:50098 >/tmp/fakecore.log 2>&1 &
    fake=$!
    trap "kill $fake 2>/dev/null" EXIT
    for _ in $(seq 50); do
      php -r "exit(@fsockopen(\"127.0.0.1\", 50099) ? 0 : 1);" && break
      sleep 0.1
    done
    export CALYX_FAKECORE_ADDR=127.0.0.1:50099 CALYX_FAKECORE_PROXY_ADDR=127.0.0.1:50098
    vendor/bin/phpunit --testsuite core "$@"
  ' sh "${@:2}"
  # The backend-8.3 image has no ext-grpc, so Composer must ignore that requirement.
  run backend-8.3 core composer install --no-interaction --no-progress --ignore-platform-req=ext-grpc
  run backend-8.3 core vendor/bin/phpunit --testsuite no-grpc
  ;;
lint-core)
  run core core composer install --no-interaction --no-progress
  run core core vendor/bin/phpstan analyse --no-progress --memory-limit=1G
  run core core vendor/bin/php-cs-fixer fix --dry-run --diff
  ;;
test-client)
  run backend-8.3 client composer install --no-interaction --no-progress
  # Extra arguments go to PHPUnit, for example: run.sh test-client --filter CurlTransportTest
  phpunit backend-8.3 "${@:2}"
  phpunit backend-8.5 "${@:2}"
  ;;
lint-client)
  run backend-8.3 client composer install --no-interaction --no-progress
  run backend-8.3 client vendor/bin/phpstan analyse --no-progress --memory-limit=1G
  run backend-8.3 client vendor/bin/php-cs-fixer fix --dry-run --diff
  ;;
deps-composer)
  # Used by deps-check.sh: fails when "require" has an entry other than php and ext-*.
  run backend-8.3 client php -r '
    $c = json_decode(file_get_contents("composer.json"), true, 512, JSON_THROW_ON_ERROR);
    $bad = array_filter(array_keys($c["require"] ?? []), fn ($n) => $n !== "php" && !str_starts_with($n, "ext-"));
    if ($bad) { fwrite(STDERR, "deps:check failed: require has packages: " . implode(", ", $bad) . "\n"); exit(1); }
  '
  ;;
archive-check)
  # Builds both zips, then installs the core from an artifact repository, as a user would.
  # The zips must not hold the tests, the examples or the tool files (.gitattributes).
  # The archive-8.3 image has ext-zip but no ext-grpc, so the install of the core ignores
  # ext-grpc. The check loads only the generated classes, which are plain PHP code.
  run archive-8.3 client sh -ec '
    check_zip() {
      listing=$(unzip -Z1 "$1")
      if echo "$listing" | grep -E "^(tests|examples|vendor)/|\.cache|composer.lock|phpunit.xml|phpstan.neon|php-cs-fixer|gitattributes"; then
        echo "archive-check failed: $1 holds a development file." >&2
        exit 1
      fi
      echo "$listing" | grep -q "^$2" || { echo "archive-check failed: no $2 in $1." >&2; exit 1; }
    }
    composer archive --format=zip --dir=/tmp/dist --no-interaction
    (cd ../core && composer archive --format=zip --dir=/tmp/dist --no-interaction)
    check_zip /tmp/dist/calyx-client-sdk-0.1.0.zip src/Generated/
    check_zip /tmp/dist/calyx-client-sdk-core-0.1.0.zip generated/Calyx/Core/V1/
    mkdir /tmp/app
    cd /tmp/app
    echo "{\"repositories\":[{\"type\":\"artifact\",\"url\":\"/tmp/dist\"}],\"minimum-stability\":\"stable\"}" > composer.json
    composer require calyx/client-sdk-core:0.1.0 --no-interaction --no-progress --ignore-platform-req=ext-grpc
    php -r "require \"vendor/autoload.php\"; echo Calyx\\Client\\Version::SDK, \"\\n\"; var_dump(class_exists(Calyx\\Core\\V1\\Event::class), class_exists(GPBMetadata\\Calyx\\Core\\V1\\Event::class));"
  '
  ;;
*)
  echo "Usage: run.sh composer | composer-core | test-core | lint-core | test-client | lint-client | deps-composer | archive-check | protoc | protoc-check" >&2
  exit 2
  ;;
esac
