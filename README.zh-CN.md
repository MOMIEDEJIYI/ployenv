# ployenv

[English](README.md) | **简体中文**

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**绿色单文件**的本地语言运行时版本管理器 —— 一套命令搞定
**Node.js / Java / Go / Python** 的下载、安装、查看与切换。

不用装安装程序、不用账号、不做许可校验、不用登录，**也不改你的系统环境**。
一个可执行文件放到哪，运行时和配置就存在哪。

```console
$ ployenv install node@20
$ ployenv use node@20
$ ployenv shell
```

## 为什么用它

- **非侵入** —— 不改系统 `PATH`、不写 `.npmrc` / `pip.ini`、不动注册表。激活只影响当前那个终端窗口。
- **显式** —— 默认走官方源；只有你明确要求时才会接入已有安装，从不自动探测。
- **可预测** —— 生效版本来自项目文件（`.ployenv`，以及 `.nvmrc`、`.tool-versions` 等兼容文件）
  或一个全局默认，可用 `PLOYENV_<语言>_VERSION` 覆盖；**绝不**回退到"随便找一个已装版本"。
- **绿色** —— 单个约 7.5 MB 的静态二进制，零第三方依赖。整个文件夹搬走即可。
- **可脚本化** —— `--json` 输出纯 ASCII（非 ASCII 字符转义为 `\uXXXX`），不受任何终端代码页影响。

## 安装

当前以 **Windows 10/11** 为主要支持目标；Linux 与 macOS（amd64/arm64）由 CI 在真机上构建并
验证。macOS 二进制未签名、未公证，首次运行可能被 Gatekeeper 拦下，需用
`xattr -d com.apple.quarantine` 放行。从源码构建需要 **Go 1.22+**。

```powershell
go build -ldflags "-s -w" -o dist/ployenv.exe ./cmd/ployenv
```

若允许执行脚本：

```powershell
powershell -ExecutionPolicy Bypass -File .\build.ps1 -Test
```

然后把可执行文件放进 `PATH`，例如 `D:\PloyEnv\ployenv.exe`。

## 快速开始

```powershell
# 看看有哪些版本
ployenv available node --limit 5

# 安装（版本写 22 / lts / latest 都可以）
ployenv install node@20
ployenv install java@21
ployenv install go@1.24
ployenv install python@3.12

# 记录默认版本，然后查看
ployenv use node@20
ployenv current
ployenv list

# 只在当前终端激活
ployenv shell
```

在项目里放一个 `.ployenv` 就能锁定版本：

```ini
node=20.20.2
java=21
go=1.24
python=3.12
```

`ployenv use node@20 --project` 会帮你写入或更新这个文件。

## 文档

完整的命令参考、配置说明与故障排查见 **[docs/manual.zh-CN.md](docs/manual.zh-CN.md)**（[English](docs/manual.md)）。

常用命令：

| 命令 | 说明 |
|---|---|
| `ployenv support` | 查看支持的运行时 |
| `ployenv available <语言>` | 查看可安装版本 |
| `ployenv doctor` | 查看路径、可写性与版本解析报告 |
| `ployenv config` | 查看或修改全部配置 |

内置摘要可以用 `ployenv --help` 查看。

## 后续规划

**已完成**

- [x] 便携数据目录 + 首次运行基础配置
- [x] Node.js / Java / Go / Python，各由一份内置 Spec 描述
- [x] 镜像回退，以及中断安全（`Ctrl+C` 不会留下残缺文件）
- [x] Go 安装包的 SHA-256 校验（node / Java / Python 的校验和待补）
- [x] 会话级激活，不改系统环境
- [x] `--json` 机器接口
- [x] 预编译包随 GitHub Releases 发布（windows / linux / darwin × amd64 / arm64，附校验和）

**计划中**

- [ ] 运行时可配置化（FR-11）。当前运行时定义是编译进二进制的 Go 代码，`config.json` 还不能新增或覆盖
- [ ] node / Java / Python 的校验和；`doctor` 增加对各个下载源的连通性检查
- [ ] 用同一套 `Spec` 定义接入更多运行时，含已预留 `kind` 字段的数据库 / 中间件（`MySQL`、`Redis`、`Nginx`）
- [ ] shell 补全脚本；`install --dry-run` 在实现前会被**拒绝**（此前是接受并忽略，会真下载安装，BUG-020）
- [ ] 按 `logs.keep` 做日志轮转

**明确不做**

- 进入目录**自动切换** —— 需要全局 shell hook，会破坏非侵入承诺。请在各个项目终端里用 `ployenv shell`。

## 许可证

[MIT](LICENSE) © MOMIEDEJIYI
