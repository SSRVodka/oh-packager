## ohloha

A simple system-level package management tool for the OpenHarmony platform, offering functionality to build system-level package repository (for hosting packages), package system-level libraries, install them into SDKs, or install them into specified directories. This facilitates developers' access to official system-level libraries not integrated into OHOS during the development phase.

一个简单的 OpenHarmony 平台系统级包管理工具，提供系统级包仓库构建、系统级库打包、安装到 SDK、安装到指定目录的功能，方便开发者开发阶段使用官方 OHOS 未集成的系统级库。

## 用法

Clone this repository:

克隆这个仓库：

```bash
git clone --recurse-submodules https://gitcode.com/openharmony-robot/tools_ohloha.git
cd tools_ohloha
```

Build with Go:

用 Go 编译器编译项目：

```bash
make
```

Now you get binaries `ohla`  (client),  `ohla-server` (server),  `ohla-tool`  (tool)  in directory `build/bin`;

现在你能在 `build/bin` 下得到包管理器的二进制程序 `ohla`（客户端）、`ohla-server`（部署和托管服务端程序）、`ohla-tool`（实用工具）

建议将它们加入 `PATH`，后续脚本会直接调用 `ohla`：

```bash
export PATH="${PWD}/build/bin:${PATH}"
```

Use `--help` for more details.

使用 `--help` 获取帮助信息。

### 使用 `ohloha` 为 OpenHarmony 设备从源码交叉编译 native 库

1. 安装交叉编译所需的系统依赖。对于 debian 系（使用 `apt` 包管理器）的宿主机，我们在 `ohloha_pkgs` 中提供了安装脚本，直接安装：

   ```sh
   pushd ohloha_pkgs
   source ./DEPS
   popd
   ```

2. 设置必要的环境变量，尖括号替换成你自己的路径：

   ```sh
   export OHOS_SDK=<your-sdk-path-for-OpenHarmony>    # 例如 .../15，需要包含 native/、toolchains/ 等目录
   export OHOS_CPU=<arch-for-your-OpenHarmony-device> # 可选 aarch64、arm、x86_64
   ```

3. 开始交叉编译和安装（在本项目根目录下执行）：

   交叉编译并安装到指定目录（举个例子）：

   ```sh
   OHOS_CPU=x86_64 ./scripts/build_and_install.sh --prefix "${PWD}/out" openssl curl
   ```

   也可以一次指定多个上层库，`ohla` 会读取 `ohloha_pkgs/PKG_INDEX.json`，根据包的 `pkg_deps` 和 `pkg_build_deps` 自动收集依赖、解析版本约束、拓扑排序，并用并发 worker 调用 `ohloha_pkgs/builder.sh --build-one --resolved-deps=...` 交叉编译。也就是说，下面命令中的 `opencv`、`ffmpeg` 及其依赖都会由 `ohla` 管理，不需要手工把依赖逐个排好序；每个 worker 会收到已解析的依赖路径，不依赖 `builder.sh` 扫描已有 dist 目录猜测版本：

   ```sh
   OHOS_CPU=x86_64 ./scripts/build_and_install.sh --prefix "${PWD}/out" opencv ffmpeg
   ```

   可以用 `--jobs` 控制并发构建数，例如：

   ```sh
   OHOS_CPU=x86_64 ./scripts/build_and_install.sh --jobs 8 --prefix "${PWD}/out" opencv ffmpeg
   ```

   或者交叉编译并直接安装到 SDK 中：

   ```sh
   ./scripts/build_and_install.sh --to-sdk openblas ffmpeg openssl
   ```

   交叉编译所有目前已迁移的库并同时安装到指定目录（`./script/out/`）和 SDK 中：

   ```sh
   ./scripts/build_all_and_install2sdk.sh
   ```

   如果只想交叉编译和打包，不安装到 SDK 或指定目录，可以不传 `--to-sdk`、`--both`、`--prefix`：

   ```sh
   OHOS_CPU=aarch64 ./scripts/build_and_install.sh openssl curl
   ```

   脚本会先确保 `build/bin` 下的 `ohla`、`ohla-tool`、`ohla-server` 可用，再执行 `ohla config --pkg-src-repo ./ohloha_pkgs` 和 `ohla xcompile --arch ${OHOS_CPU} --jobs N ...`，最后把 `ohloha_pkgs/dist.<arch>.<pkg>-<version>` 打成可被 `ohla add` 安装的包。构建器不再自动维护 `ohloha_pkgs/dist.<arch>.<pkg>` legacy alias；如外部流程确实需要该路径，应自行创建软链接并明确其对应版本。

   更多用法：

   ```sh
   ./scripts/build_and_install.sh --help
   ```

   你也可以阅读 `./scripts/build_and_install.sh` 来了解 `ohla` / `ohla-server` 是如何使用的。

4. （可选）注意：如果安装的位置需要移动，但又希望移动后的库能继续开发使用（如用来交叉编译其他库），您需要 patch 库安装的 config path，这包含了 CMake configs (`*.cmake`)、Library Archives (`*.la`)、PkgConfig (`*.pc`) 等路径信息。您需要执行：

   ```sh
   ohla patch <current-prefix-path> <new-prefix-path>
   ```

   举个例子，如果当前安装的位置是 `./scripts/out`，但是我因为某些原因希望将它整体移动到 `/home/xhw/test` 下面作为开发使用，那么为了 OH 编译工具链能够找到这些库，你需要先 `ohla patch ./scripts/out /home/xhw/test` 再移动和开发；



### 一些注意事项和 Q&A

- 如果我想迁移一些别的系统库（这个仓库里没有的），如何加到这个仓库里？如何迁移？

  答：这个仓库是包管理的仓库，如果需要添加迁移别的库，您可以参见子模块 [`ohloha_pkgs`](https://gitcode.com/openharmony-robot/tools_ohloha_pkgs#%E6%B7%BB%E5%8A%A0-%E5%90%91-oh-%E8%BF%81%E7%A7%BB-%E6%96%B0%E7%9A%84%E5%BA%93) 的 README 文档，按规约向 `ohloha_pkgs/` 下添加构建文件，再次编译即可；

- 交叉编译后，编译的缓存放在以下位置：

  - 每个包的编译输出目录：`./ohloha_pkgs/dist.<arch>.*`；
  - Python wheels 编译输出目录：`./ohloha_pkgs/dist.wheels/`；
  - 下载缓存、源码快照、构建 workdir、artifact、host 工具、crossenv 等构建状态：`./ohloha_pkgs/.ohloha/`；
  - 旧版本可能残留 `.staging*` 或 `crossenv_*` 目录；当前构建流程不再把它们作为源码或 crossenv 状态来源。

  如果交叉编译过程中发现无法解决的问题，可以通过删除这些目录来尝试一下；




#### 作为 Server 将编好的库分发出去

将编好的库打包：

```shell
ohla-tool -a aarch64 --api 15 -n console_bridge -i ./dist.aarch64.console_bridge -v 0.0.1
```

创建一个二进制仓库：

```shell
ohla-server init --repo ./repo
```

把编好的包部署到仓库目录下：

```shell
ohla-server deploy ./console_bridge-0.0.1-aarch64-api15.pkg ./console_bridge-0.0.1-aarch64-api15.json --repo ./repo
```

现在启动一个文件服务器将 `repo` 目录暴露出去（如 NginX），Client 即可使用这些编好的库。使用方法参见下节。

#### 从 Server 直接下载编译好的库

Client 端设置存放已编好的包的仓库地址：

```shell
ohla config -a aarch64 -d <your sdk directory> -s <repository URL>
```


Client 端查看当前设置的包的仓库有哪些已编译的包：

```shell
ohla list
```

> [!WARNING]
>
> The installation is irreversible for now. Make sure to double check your prefix or backup your SDK if needed.
>
> 注意下面的安装过程不可逆。如果需要你的 SDK 不被更改，请及时备份。

从仓库安装指定包（以 `console_bridge` 为例）到指定目录：

```shell
ohla add console_bridge --prefix ./dist
```

从仓库安装指定包到 SDK（不可逆）：

```shell
ohla add console_bridge
```
