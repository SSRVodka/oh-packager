#!/bin/bash
set -Eeuo pipefail

SCRIPT_DIR=$(dirname "$(readlink -f "$0")")
PROJECT_ROOT=$(dirname "$SCRIPT_DIR")
SRC_REPO="${PROJECT_ROOT}/ohloha_pkgs"
JOBS="${OHLOHA_JOBS:-$(nproc 2>/dev/null || echo 1)}"

"${SCRIPT_DIR}/build_and_install.sh" --jobs "$JOBS" --both --prefix "${SCRIPT_DIR}/out" \
    libz openssl libffi sqlite bzip2 xz libncursesw libreadline libgettext util-linux python3 \
    openblas libaacplus x264 alsa-lib libiconv ffmpeg \
    libgmp libmpfr libmpc binutils patchelf \
    python3-cython python3-build 'python3-numpy>=2' \
    python3-wheel python3-setuptools opencv python3-opencv python3-netifaces python3-pillow \
    icu libxml2 asio console_bridge nasm attr acl assimp fmt yaml-cpp libpsl curl boost eigen qhull libccd \
    SuiteSparse gflags glog gtest ceres-solver zstd zeromq libexpat libpng g2o geographiclib tinyxml2 \
    oneTBB pcre2 swig YDLidar-SDK llama.cpp rsync lz4 octomap xtl xtensor xsimd nanoflann nlohmann-json \
    llvm freetype fontconfig libdrm SPIRV-Headers SPIRV-Tools SPIRV-LLVM-Translator glslang xorg mesa glu \
    ogre GraphicsMagick flann pcl bullet3 qt5 \
    grpc glew glut gdb vim openssh-portable \
    libpcap lua pixman cairo cups openjdk

if [ -z "${OHOS_CPU:-}" ]; then
    OHOS_CPU=aarch64
fi

pydeps_url="https://github.com/SSRVodka/oh-edu-pkgs/releases/download/v0.0.2/pydeps-${OHOS_CPU}-20251229.tar.gz"
pydeps_archive="${SCRIPT_DIR}/pydeps-${OHOS_CPU}.tar.gz"

echo ">>> Fetching prebuilt Python dependencies: ${pydeps_url}"
wget -O "$pydeps_archive" "$pydeps_url"
tar -C "$SCRIPT_DIR" -zxpvf "$pydeps_archive"
tar -C "$SCRIPT_DIR" -zcpvf "${PROJECT_ROOT}/ohos-18-sysdeps-${OHOS_CPU}-$(date +"%Y%m%d%H%M%S").tar.gz" out/

echo ">>> Build/install-all flow completed. Source repo: ${SRC_REPO}"
