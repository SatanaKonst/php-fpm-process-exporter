#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: build_release.sh VERSION [options]

Builds release binaries and release archives for php-fpm-process-exporter.

Arguments:
  VERSION             Release version, for example v1.0.0

Options:
  --targets LIST      Comma-separated target list (default: linux/amd64,linux/arm64)
  --dist DIR          Output directory (default: dist)
  --help              Show help

Output layout:
  dist/VERSION/
    php-fpm-process-exporter_VERSION_linux_amd64
    php-fpm-process-exporter_VERSION_linux_amd64.tar.gz
    php-fpm-process-exporter_VERSION_linux_arm64
    php-fpm-process-exporter_VERSION_linux_arm64.tar.gz
    checksums.txt
EOF
}

if [[ ${1:-} == "-h" || ${1:-} == "--help" || $# -lt 1 ]]; then
  usage
  exit 0
fi

VERSION="$1"
shift

TARGETS="linux/amd64,linux/arm64"
DIST_DIR="dist"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --targets)
      TARGETS="${2:?missing value for --targets}"
      shift 2
      ;;
    --dist)
      DIST_DIR="${2:?missing value for --dist}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC_DIR="$ROOT_DIR/src"
if [[ "$DIST_DIR" = /* ]]; then
  DIST_BASE="$DIST_DIR"
else
  DIST_BASE="$ROOT_DIR/$DIST_DIR"
fi
OUT_DIR="$DIST_BASE/$VERSION"

if [[ ! -f "$SRC_DIR/go.mod" ]]; then
  echo "go.mod not found in $SRC_DIR" >&2
  exit 1
fi

mkdir -p "$OUT_DIR"

if ! command -v go >/dev/null 2>&1; then
  echo "go is required to build release artifacts" >&2
  exit 1
fi

build_target() {
  local os="$1"
  local arch="$2"
  local bin_name="php-fpm-process-exporter_${VERSION}_${os}_${arch}"
  local bin_path="$OUT_DIR/$bin_name"
  local stage_dir="$OUT_DIR/stage/${os}_${arch}"
  local archive_name="${bin_name}.tar.gz"

  rm -rf "$stage_dir"
  mkdir -p "$stage_dir"

  (
    cd "$SRC_DIR"
    GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -o "$bin_path" .
  )

  install -m 0644 "$SRC_DIR/config.example.json" "$stage_dir/config.example.json"
  install -m 0644 "$ROOT_DIR/grafana-dashboard.json" "$stage_dir/grafana-dashboard.json"
  install -m 0644 "$SRC_DIR/php-fpm-process-exporter.service" "$stage_dir/php-fpm-process-exporter.service"
  install -m 0644 "$ROOT_DIR/install_ubuntu.sh" "$stage_dir/install_ubuntu.sh"
  install -m 0644 "$ROOT_DIR/README.md" "$stage_dir/README.md"
  install -m 0755 "$bin_path" "$stage_dir/php-fpm-process-exporter"

  (
    cd "$stage_dir"
    tar -czf "$OUT_DIR/$archive_name" \
      php-fpm-process-exporter \
      config.example.json \
      grafana-dashboard.json \
      php-fpm-process-exporter.service \
      install_ubuntu.sh \
      README.md
  )

  rm -rf "$stage_dir"
}

IFS=',' read -r -a TARGET_ARRAY <<< "$TARGETS"
for target in "${TARGET_ARRAY[@]}"; do
  os="${target%/*}"
  arch="${target#*/}"
  case "$os" in
    linux) ;;
    *)
      echo "Unsupported OS in target: $target" >&2
      exit 1
      ;;
  esac
  case "$arch" in
    amd64|arm64) ;;
    *)
      echo "Unsupported architecture in target: $target" >&2
      exit 1
      ;;
  esac
  build_target "$os" "$arch"
done

(
  cd "$OUT_DIR"
  if command -v sha256sum >/dev/null 2>&1; then
    find . -maxdepth 1 -type f ! -name 'checksums.txt' -print0 | sort -z | xargs -0 sha256sum > checksums.txt
  else
    find . -maxdepth 1 -type f ! -name 'checksums.txt' -print0 | sort -z | xargs -0 shasum -a 256 > checksums.txt
  fi
)

echo "Release artifacts written to $OUT_DIR"
