#!/bin/bash
set -Eeuo pipefail

SCRIPT_DIR=$(dirname "$(readlink -f "$0")")
PROJECT_ROOT=$(dirname "$SCRIPT_DIR")
SRC_REPO="${PROJECT_ROOT}/ohloha_pkgs"
BIN_DIR="${PROJECT_ROOT}/build/bin"

PACKAGES=()
SKIP_XCOMPILE=0
INSTALL_MODE=""   # "sdk" | "prefix" | "both" | ""
PREFIX_PATH=""
JOBS="${OHLOHA_JOBS:-$(nproc 2>/dev/null || echo 1)}"
KEEP_GOING=0

print_usage() {
    cat <<EOF
Usage: $0 [--help] [--skip-xcompile] [--jobs N] [--keep-going] [--to-sdk | --both | --prefix <path>] <pkg1> [pkg2] ...

Options:
  --jobs, -j N        Number of parallel xcompile jobs. Defaults to nproc or OHLOHA_JOBS.
  --keep-going        Continue building independent packages after a failure.
  --skip-xcompile     Reuse existing dist/artifact outputs and only deploy/install.
  --to-sdk            Install generated packages into OHOS_SDK.
  --prefix <path>     Install generated packages into a prefix.
  --both              Install into both OHOS_SDK and prefix. Defaults prefix to scripts/out.
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --skip-xcompile)
            SKIP_XCOMPILE=1
            shift
            ;;
        --jobs|-j)
            if [[ -n "${2:-}" && ! "$2" =~ ^-- ]]; then
                JOBS="$2"
                shift 2
            else
                echo "ERROR: missing value for $1"
                exit 1
            fi
            ;;
        --keep-going)
            KEEP_GOING=1
            shift
            ;;
        --to-sdk)
            INSTALL_MODE="sdk"
            shift
            ;;
        --both)
            INSTALL_MODE="both"
            shift
            ;;
        --prefix)
            if [[ -n "${2:-}" && ! "$2" =~ ^-- ]]; then
                PREFIX_PATH="$2"
                if [[ "${INSTALL_MODE}" == "sdk" || "${INSTALL_MODE}" == "both" ]]; then
                    INSTALL_MODE="both"
                else
                    INSTALL_MODE="prefix"
                fi
                shift 2
            else
                echo "ERROR: missing value for --prefix. Example: --prefix /my/custom/path"
                exit 1
            fi
            ;;
        --help|-h)
            print_usage
            exit 0
            ;;
        -*)
            echo "ERROR: Unrecognized option: '$1'"
            print_usage
            exit 1
            ;;
        *)
            PACKAGES+=("$1")
            shift
            ;;
    esac
done

if [[ ! "$JOBS" =~ ^[0-9]+$ ]] || [ "$JOBS" -lt 1 ]; then
    echo "ERROR: --jobs must be a positive integer"
    exit 1
fi

if [[ "${INSTALL_MODE}" == "both" && -z "${PREFIX_PATH}" ]]; then
    PREFIX_PATH="${SCRIPT_DIR}/out"
    echo "INFO: --prefix not specified for --both. Use ${PREFIX_PATH} by default"
fi
if [[ "${INSTALL_MODE}" == "prefix" && -z "${PREFIX_PATH}" ]]; then
    echo "ERROR: missing value for --prefix"
    exit 1
fi
if [ "${#PACKAGES[@]}" -eq 0 ]; then
    echo "ERROR: no package requested"
    print_usage
    exit 1
fi

if [ -z "${OHOS_SDK:-}" ]; then
    echo "ERROR: OHOS_SDK not set. e.g., export OHOS_SDK=/path/to/oh_sdk/18"
    exit 1
fi
if [ ! -f "${OHOS_SDK}/toolchains/oh-uni-package.json" ]; then
    echo "ERROR: invalid OHOS_SDK: ${OHOS_SDK}/toolchains/oh-uni-package.json not found"
    exit 1
fi
OHOS_SDK_API_VERSION=$(python3 - "$OHOS_SDK/toolchains/oh-uni-package.json" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as f:
    print(json.load(f).get("apiVersion", ""))
PY
)

if [ -z "${OHOS_CPU:-}" ]; then
    OHOS_CPU=aarch64
    echo "INFO: OHOS_CPU not set; using ${OHOS_CPU}"
fi
case "${OHOS_CPU}" in
    aarch64) OHOS_ARCH="arm64-v8a" ;;
    arm) OHOS_ARCH="armeabi-v7a" ;;
    x86_64) OHOS_ARCH="x86_64" ;;
    *) echo "ERROR: Unsupported OHOS_CPU '${OHOS_CPU}' (aarch64|arm|x86_64)"; exit 1 ;;
esac
export OHOS_CPU OHOS_ARCH OHOS_SDK

ensure_tools() {
    if [ ! -x "${BIN_DIR}/ohla" ] || [ ! -x "${BIN_DIR}/ohla-tool" ] || [ ! -x "${BIN_DIR}/ohla-server" ]; then
        echo ">>> Building ohla tools..."
        make -C "$PROJECT_ROOT" all
    fi
    export PATH="${BIN_DIR}:$PATH"
}

ensure_tools

PKG_MGR="${PKG_MGR:-ohla}"
DEPLOY_DIR="${SRC_REPO}/deploy"
rm -rf "$DEPLOY_DIR"

"$PKG_MGR" config \
    --arch "${OHOS_CPU}" \
    --ohos-sdk "${OHOS_SDK}" \
    --pkg-src-repo "${SRC_REPO}" \
    --server-root http://localhost

if [ "$SKIP_XCOMPILE" -eq 0 ]; then
    xcompile_args=(xcompile --arch "${OHOS_CPU}" --jobs "${JOBS}")
    if [ "$KEEP_GOING" -eq 1 ]; then
        xcompile_args+=(--keep-going)
    fi
    echo ">>> Cross-compiling (${JOBS} jobs): ${PACKAGES[*]}"
    "$PKG_MGR" "${xcompile_args[@]}" "${PACKAGES[@]}"
else
    echo ">>> Skipping cross-compiling process"
fi

echo ">>> Deploying built packages..."
"${SRC_REPO}/pkgs-deploy-all.sh"

pkg_files=()
if [ ! -d "$DEPLOY_DIR" ]; then
    echo "ERROR: deploy directory not found: $DEPLOY_DIR"
    exit 1
fi
while IFS= read -r -d '' file; do
    name=$(basename -- "$file" .json)
    abs_dir=$(dirname -- "$(realpath -- "$file")")
    pkg_path="${abs_dir}/${name}.pkg"
    if [ -f "$pkg_path" ]; then
        pkg_files+=("$pkg_path")
    fi
done < <(find "$DEPLOY_DIR" -maxdepth 1 -name "*.json" -print0)

if [ "${#pkg_files[@]}" -eq 0 ]; then
    echo "ERROR: no *.pkg found in $DEPLOY_DIR"
    exit 1
fi

if [ -z "${INSTALL_MODE}" ]; then
    echo "INFO: no --to-sdk / --both / --prefix specified. Skipping installation."
    echo ">>> Finished cross compiling and packaging: ${PACKAGES[*]}"
    exit 0
fi

if [[ "${INSTALL_MODE}" == "sdk" || "${INSTALL_MODE}" == "both" ]]; then
    if [ "${OHOS_SDK}" = "/" ] || [ ! -d "${OHOS_SDK}" ]; then
        echo "ERROR: invalid OHOS_SDK path: '${OHOS_SDK}'" >&2
        exit 1
    fi

    sdk_bak="${OHOS_SDK}-backup-$(date +"%Y%m%d%H%M%S")"
    if [ -e "${sdk_bak}" ]; then
        echo "ERROR: backup target already exists: ${sdk_bak}" >&2
        exit 1
    fi

    restore_on_failure() {
        local err_code=$?
        echo "ERROR: SDK installation failed (code: ${err_code}); attempting recovery" >&2
        if [ ! -e "${OHOS_SDK}" ] && [ -d "${sdk_bak}" ]; then
            mv "${sdk_bak}" "${OHOS_SDK}"
        fi
        exit "$err_code"
    }
    trap restore_on_failure ERR

    echo ">>> Backing up SDK to ${sdk_bak}"
    cp -a "${OHOS_SDK}" "${sdk_bak}"

    echo ">>> Installing packages to SDK: ${OHOS_SDK}"
    "$PKG_MGR" add --no-resolve -y "${pkg_files[@]}"

    pushd "${OHOS_SDK}/.." >/dev/null
    sdk_archive="ohos-sdk-${OHOS_SDK_API_VERSION}-linux-${OHOS_CPU}-$(date +"%Y%m%d%H%M%S").tar.gz"
    tar -zcpvf "$sdk_archive" "$(basename "${OHOS_SDK}")"
    popd >/dev/null
    mv "${OHOS_SDK}/../${sdk_archive}" "${PROJECT_ROOT}/"

    rm -rf "${OHOS_SDK:?}/"*
    shopt -s dotglob nullglob
    mv "${sdk_bak}/"* "${OHOS_SDK}/"
    shopt -u dotglob nullglob
    rmdir "${sdk_bak}"
    trap - ERR

    echo ">>> SDK package archive written to ${PROJECT_ROOT}/${sdk_archive}"
fi

if [[ "${INSTALL_MODE}" == "prefix" || "${INSTALL_MODE}" == "both" ]]; then
    mkdir -p "${PREFIX_PATH}"
    echo ">>> Installing packages to prefix: ${PREFIX_PATH}"
    "$PKG_MGR" add --no-resolve -y --prefix "${PREFIX_PATH}" "${pkg_files[@]}"
    echo ">>> Prefix installation completed successfully."
fi

echo ">>> All tasks done."
