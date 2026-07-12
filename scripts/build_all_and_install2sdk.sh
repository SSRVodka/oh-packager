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
    python3-av python3-bcrypt python3-cffi python3-charset-normalizer python3-contourpy \
    python3-pycrypto python3-cryptography python3-fonttools python3-greenlet \
    python3-grpcio python3-grpcio-tools python3-kiwisolver python3-lmdb python3-lxml \
    python3-markupsafe python3-matplotlib python3-pynacl python3-naked python3-pandas \
    python3-psutil python3-pyclipper python3-pymupdf python3-pyyaml python3-rapidfuzz \
    python3-scipy python3-shapely python3-sqlalchemy python3-attrs python3-beautifulsoup4 \
    python3-blinker python3-click python3-cycler python3-python-dateutil python3-distro \
    python3-ffmpeg-python python3-flask python3-imageio python3-imutils python3-itsdangerous \
    python3-jinja2 python3-jsonlines python3-pyjwt python3-more-itertools python3-mpmath \
    python3-packaging python3-pika python3-pycparser python3-pymysql python3-pyparsing \
    python3-pyserial python3-pysocks python3-pytz python3-rarfile python3-requests \
    python3-scapy python3-shellescape python3-six python3-socksio python3-soupsieve \
    python3-sympy python3-termcolor python3-urllib3 python3-xmltodict \
    python3-openai python3-rpds python3-transformers \
    icu libxml2 asio console_bridge nasm attr acl assimp fmt yaml-cpp libpsl curl boost eigen qhull libccd \
    SuiteSparse gflags glog gtest ceres-solver zstd zeromq libexpat libpng g2o geographiclib tinyxml2 \
    oneTBB pcre2 swig YDLidar-SDK llama.cpp rsync lz4 octomap xtl xtensor xsimd nanoflann nlohmann-json \
    llvm freetype fontconfig libdrm SPIRV-Headers SPIRV-Tools SPIRV-LLVM-Translator glslang xorg mesa glu \
    ogre GraphicsMagick flann pcl bullet3 qt5 \
    grpc glew glut gdb vim openssh-portable \
    libpcap lua pixman cairo cups openjdk \
    bash tree zsh

if [ -z "${OHOS_CPU:-}" ]; then
    OHOS_CPU=aarch64
fi

#pydeps_url="https://github.com/SSRVodka/oh-edu-pkgs/releases/download/v0.0.2/pydeps-${OHOS_CPU}-20251229.tar.gz"
#pydeps_archive="${SCRIPT_DIR}/pydeps-${OHOS_CPU}.tar.gz"
#
#echo ">>> Fetching prebuilt Python dependencies: ${pydeps_url}"
#wget -O "$pydeps_archive" "$pydeps_url"
#tar -C "$SCRIPT_DIR" -zxpvf "$pydeps_archive"
tar -C "$SCRIPT_DIR" -zcpvf "${PROJECT_ROOT}/ohos-18-sysdeps-${OHOS_CPU}-$(date +"%Y%m%d%H%M%S").tar.gz" out/

echo ">>> Build/install-all flow completed. Source repo: ${SRC_REPO}"
