#!/bin/bash

set -Eeuo pipefail

cd $(dirname $(readlink -f $0))

SRC_REPO=$(pwd)/../ohloha_pkgs

if [ -z "${OHOS_SDK:-}" ]; then
    echo "ERROR: OHOS_SDK not set. e.g., export OHOS_SDK=/xxx/linux"
    exit 1
fi

if [ -z "${OHOS_CPU:-}" ]; then
    echo "ERROR: OHOS_CPU not set (aarch64|arm|x86_64). e.g., export OHOS_CPU=aarch64"
    exit 1
fi

if [ "${OHOS_CPU}" = "aarch64" ]; then
    OHOS_ARCH="arm64-v8a"
elif [ "${OHOS_CPU}" = "arm" ]; then
    OHOS_ARCH="armeabi-v7a"
elif [ "${OHOS_CPU}" = "x86_64" ]; then
    OHOS_ARCH="x86_64"
else
    echo "ERROR: Unsupported cpu '$OHOS_CPU' (supported 'aarch64', 'arm', 'x86_64')"
    exit 1
fi

PKG_MGR=ohla
PKG_SERVER=ohla-server

$SRC_REPO/gen-versions.sh

$PKG_MGR \
    config --arch ${OHOS_CPU} --ohos-sdk ${OHOS_SDK} --pkg-src-repo ${SRC_REPO} --server-root http://localhost

$PKG_MGR \
    xcompile --arch ${OHOS_CPU} \
    libz openssl libffi sqlite bzip2 xz libncursesw libreadline libgettext util-linux python3 \
    openblas libaacplus x264 alsa-lib libiconv ffmpeg \
    libgmp libmpfr libmpc binutils patchelf \
    python3-cython python3-build python3-numpy2 \
    python3-wheel python3-setuptools opencv python3-opencv \
    icu libxml2 asio console_bridge nasm attr acl assimp fmt yaml-cpp libpsl curl boost eigen qhull libccd \
    SuiteSparse gflags glog gtest ceres-solver zstd zeromq libexpat libpng g2o geographiclib tinyxml2 \
    oneTBB pcre2 swig YDLidar-SDK llama.cpp rsync lz4 octomap xtl xtensor xsimd nanoflann nlohmann-json \
    llvm freetype fontconfig libdrm SPIRV-Headers SPIRV-Tools SPIRV-LLVM-Translator glslang xorg mesa glu \
    ogre GraphicsMagick flann pcl bullet3 qt5
    
OHOS_CPU=${OHOS_CPU} OHOS_ARCH=${OHOS_ARCH} $SRC_REPO/pkgs-deploy-all.sh

pkg_files=()
deploy_dir=$SRC_REPO/deploy

while IFS= read -r -d '' file; do
    name=$(basename -- "$file" .json)
    abs_dir=$(dirname -- "$(realpath -- "$file")")
    pkg_path="${abs_dir}/${name}.pkg"
    pkg_files+=("$pkg_path")
done < <(find "$deploy_dir" -maxdepth 1 -name "*.json" -print0)

# use --prefix to install to specific location
$PKG_MGR add --no-resolve -y "${pkg_files[@]}"
