## OH Packager

A simple system-level package management tool for the OpenHarmony platform, offering functionality to build system-level package repository (for hosting packages), package system-level libraries, install them into SDKs, or install them into specified directories. This facilitates developers' access to official system-level libraries not integrated into OHOS during the development phase.

一个简单的 OpenHarmony 平台系统级包管理工具，提供系统级包仓库构建、系统级库打包、安装到 SDK、安装到指定目录的功能，方便开发者开发阶段使用官方 OHOS 未集成的系统级库。

### Usage

Use `--help` for more details.


#### Server side

Package a compiled library:

```shell
oh-pkgtool -a aarch64 -n console_bridge -i ./compiled_lib -v 0.0.1
```

Create a hosting repository:

```shell
oh-pkgserver init --repo ./repo
```

Deploy a package to the repository:

```shell
oh-pkgserver deploy ./console_bridge-0.0.1-aarch64.pkg ./console_bridge-0.0.1-aarch64.json --repo ./repo
```


#### Client side

Configure your client first:

```shell
oh-pkgmgr -a aarch64 -d <your sdk directory> -s <repository URL>
```

Check package list on the repository:

```shell
oh-pkgmgr list
```

Install the package from the repository:

```shell
oh-pkgmgr install console_bridge --prefix ./dist
```

Install the package to OpenHarmony SDK:

```shell
oh-pkgmgr add2sdk console_bridge
```
