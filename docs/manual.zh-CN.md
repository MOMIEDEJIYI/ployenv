# ployenv 操作手册

[English](manual.md) | **简体中文**

ployenv 的完整命令参考。快速了解请看 [README](../README.zh-CN.md)。

- [概念](#概念)
- [数据目录](#数据目录)
- [命令](#命令)
- [版本解析](#版本解析)
- [会话激活](#会话激活)
- [配置](#配置)
- [镜像](#镜像)
- [脚本与编码](#脚本与编码)
- [退出码](#退出码)
- [故障排查](#故障排查)

---

## 概念

ployenv 管理的是**语言运行时**，用短名称标识：

| 名称 | 别名 | 会被加入 `PATH` 的工具 |
|---|---|---|
| `node` | `nodejs`、`node.js` | `node`、`npm`、`npx`、`corepack` |
| `java` | `jdk`、`openjdk`、`temurin` | `java`、`javac`、`jar`、`jshell`、`keytool` |
| `go` | `golang` | `go`、`gofmt` |
| `python` | `py`、`cpython` | `python`、`python3`、`pip`、`pip3` |

每个运行时都装在 `<数据目录>/versions/<名称>/<版本>/` 下，各自独立。
ployenv 从不做系统级安装，也从不改写不属于自己的文件。

版本写法有三种：

| 写法 | 含义 | 示例 |
|---|---|---|
| 精确版本 | 完整版本号 | `ployenv install node@20.20.2` |
| 部分版本 | 取匹配到的最高版本 | `ployenv install node@20` → `20.20.2` |
| 通道 | 联网查询版本索引解析 | `ployenv install node@lts`、`node@latest` |

## 数据目录

数据目录里装着 ployenv 拥有的全部东西：

```text
<数据目录>\
├─ ployenv.exe                     主程序（绿色模式下）
├─ config.json                     配置
├─ versions\<名称>\<版本>\         已安装的运行时
├─ cache\                          下载的安装包
└─ logs\                           诊断日志
```

解析顺序：

1. `PLOYENV_ROOT`（若已设置且非空）。
2. 可执行文件所在目录（若可写）。
3. 回退到 `%LOCALAPPDATA%\Programs\PloyEnv`（Windows）或 `$XDG_DATA_HOME` / `~/.local/share/ployenv`（其他平台）。

`ployenv doctor` 会打印当前使用的目录、选择它的原因，以及是否可写。

**首次运行。** 当 `config.json` 不存在时，ployenv 会执行一次基础配置：打印探测到的数据目录，确认后再写入配置。在管道 / 非 TTY 环境，或加了 `--yes` 时，自动采用默认值继续。

## 命令

### 查询

| 命令 | 说明 |
|---|---|
| `ployenv support` | 列出 ployenv 支持的运行时，含别名、工具、镜像与已安装版本。 |
| `ployenv list [语言]` | 已安装版本，并标出当前生效的那个。不指定语言时，只显示确实装了东西的运行时。 |
| `ployenv available <语言> [过滤]` | 可安装版本。`过滤` 可写 `22`、`lts`、`3.12` 等。默认只显示最新 20 个，用 `--limit N` 或 `--all` 调整。 |
| `ployenv current [语言]` | 每个已配置运行时的生效版本及其来源（`project` / `global` / `env`）。 |
| `ployenv which <工具>` | 该工具实际会执行的程序的绝对路径。 |
| `ployenv doctor` | 自检：路径、可写性、目录状态与版本解析报告。 |

### 安装与卸载

| 命令 | 说明 |
|---|---|
| `ployenv install <语言>@<版本>` | 解析、下载、校验、解包。 |
| `ployenv install <语言>@<版本> --force` | 即使已存在也重新安装。 |
| `ployenv install <语言>@<版本> --from <目录>` | 接入已有安装，不下载。 |
| `ployenv adopt <语言>[@版本] --root <目录>` | 显式接入一个目录。不写 `@版本` 时从目录名推断。 |
| `ployenv uninstall <语言>@<版本>` | 卸载某个版本。接入来的目录只解除登记，绝不删除原目录。 |
| `ployenv uninstall <语言> --all` | 卸载该语言的全部已安装版本。 |

安装流程：

1. 把请求解析成一个精确版本。
2. 把安装包下载到 `cache/`（已缓存则跳过）。
3. 上游提供校验和时进行校验。
4. 解包到 `versions/<语言>/<版本>/`；若压缩包带顶层目录则拍平。
5. 检查主程序是否存在；失败自动回滚。
6. 除非 `cache.keep` 为 `true`，否则删除安装包。

### 选择版本

| 命令 | 说明 |
|---|---|
| `ployenv use <语言>@<版本>` | 写入 `config.json`，作为全局默认版本。 |
| `ployenv use <语言>@<版本> --project` | 写入当前目录的 `.ployenv`。 |

`use` 只是记录版本。要让它在某个终端里生效，需要激活该会话（见下）。

### 会话激活

| 命令 | 说明 |
|---|---|
| `ployenv shell [--shell pwsh\|cmd\|bash]` | 打开一个已应用环境的子终端，窗口标题设为 `[ployenv] <语言>@<版本> - <目录>`。 |
| `ployenv env [--shell pwsh\|cmd\|bash\|json]` | 打印一段应用环境的脚本，由你在当前 shell 里 eval。 |
| `ployenv env --json` | 以 JSON 输出同样的信息。 |

示例：

```powershell
# PowerShell
ployenv env | Invoke-Expression
```

```bash
# bash / zsh
eval "$(ployenv env --shell bash)"
```

```bat
:: cmd 没有 eval，请改为打开一个已激活的子终端
ployenv shell
```

### 缓存

| 命令 | 说明 |
|---|---|
| `ployenv cache` | 列出缓存的安装包及总大小。 |
| `ployenv cache --clean` | 删除全部缓存的安装包。 |

未完成的下载（`.part` 文件）会在按 `Ctrl+C` 时删除；若上次进程被强杀，也会在下次运行时被清理。

## 版本解析

解析只有三级，结果始终可预测：

1. **环境变量覆盖** —— `PLOYENV_<语言>_VERSION`，例如 `PLOYENV_NODE_VERSION=20.20.2`。优先级最高，给 CI 用。
2. **项目文件** —— 从当前目录逐级向上查找 `.ployenv`。同时只读兼容 `.nvmrc`、`.node-version`、`.java-version`、`.python-version`、`.go-version`、`.tool-versions`、`go.mod`。
3. **全局默认** —— `config.json` 的 `defaults` 段。

说明：

- 只有**已配置**的运行时才会出现在 `list` / `current` / `env` / `shell` 里。
- 项目文件里声明了但没安装的版本会**报错**，不会静默跳过：提示里会指明是哪个文件要求的。
- 项目文件格式为 `key=value`，一行一个：

  ```ini
  node=20.20.2
  java=21
  go=1.24
  python=3.12
  ```

## 配置

`ployenv config` 会打印全部配置项：

```console
$ ployenv config
root     C:\tools\PloyEnv
config   C:\tools\PloyEnv\config.json
defaults:
  node     (not set)
  java     (not set)
  go       1.27.2
  python   (not set)
mirrors:
  node     https://nodejs.org/dist (official)
  java     https://mirrors.tuna.tsinghua.edu.cn/Adoptium
  go       https://go.dev/dl (official)
  python   https://api.nuget.org/v3-flatcontainer (official)
logs:
  mode     on-error   (off | on-error | always)
  dir      logs
  keep     20
cache:
  keep     false   (true = keep downloaded archives)

change: config --set-default | --set-mirror | --set-log-mode | --set-cache-keep
        or edit config.json directly.
```

| 参数 | 作用 |
|---|---|
| `--set-default <语言> <版本>` | 设置全局默认版本。写 `none`、`off` 或 `default` 取消该默认值。 |
| `--set-mirror <语言> <地址>` | 为该运行时设置镜像。写 `none`、`off` 或 `default` 恢复官方源。 |
| `--set-log-mode off\|on-error\|always` | 诊断日志何时写入 `logs/`：从不、仅失败时、始终。 |
| `--set-cache-keep true\|false` | 安装成功后是否保留下载的安装包。 |

`config.json` 就是普通 JSON，可以手动编辑。

```powershell
ployenv config --set-default node 22.23.3    # 设置全局默认版本
ployenv config --set-default node none       # 取消它（也可写 off / default）
```

## 镜像

- 默认使用官方源。
- 配置了镜像则优先使用；失败时回退官方源，最后还可回退到系统 `curl`。
- 版本列表与下载可能使用不同端点：Java / Python 通过官方 API 取版本列表，而下载可以走镜像。
- ployenv 绝不修改系统的 npm / pip 配置或环境变量。

布局要求：

| 运行时 | 期望的镜像基址 |
|---|---|
| `node` | Node.js dist 镜像，`<基址>/v<版本>/<文件>`。 |
| `go` | Go 下载镜像，`<基址>/<文件>`。 |
| `java` | Adoptium 风格的镜像，`<基址>/<主版本>/jdk/<arch>/<os>/<文件>`。 |
| `python` | Windows：NuGet flat-container 基址。其他平台：GitHub Release 镜像，`<基址>/<tag>/<文件>`。 |

```powershell
ployenv config --set-mirror node https://registry.npmmirror.com/-/binary/node
ployenv config --set-mirror java https://mirrors.tuna.tsinghua.edu.cn/Adoptium
ployenv config --set-mirror java none        # 恢复官方源
```

## 脚本与编码

- Windows 下运行时会自动把控制台输出代码页切到 UTF-8，退出时还原，因此含中文的路径在默认 GBK 控制台里也能正常显示。
- **脚本里请用 `--json`。** 它输出纯 ASCII，非 ASCII 字符转义为 `\uXXXX`，与终端编码无关。
- 结果走 **stdout**，进度、警告与错误走 **stderr**，因此 `ployenv ... | 其他工具` 不会被污染。

全局选项：`--json`、`--verbose`、`--debug`、`-q`、`--log`、`--no-color`、`--yes`。

## 退出码

| 退出码 | 含义 |
|---|---|
| `0` | 成功。 |
| `1` | 任意错误（用法、网络、文件系统）。错误信息输出到 stderr。 |
| `130` | 被 `Ctrl+C` 中断；未完成的下载会被清理。 |

## 故障排查

**`ployenv: command not found` / 不在 `PATH` 上。**
执行 `ployenv doctor`，它会提示应该把哪个目录加入 `PATH`。

**`error: <工具> has no version configured`。**
还没有项目文件或全局默认版本：先 `ployenv use <语言>@<版本>`，再激活会话。

**项目文件要求的版本没有安装。**
错误信息里会指明是哪个文件。用 `ployenv install <语言>@<版本>` 安装，或 `adopt` 接入已有目录。

**代理环境下下载失败。**
设置标准的 `HTTP_PROXY` / `HTTPS_PROXY` 环境变量，或用 `PLOYENV_CURL` 指向一个已经配好代理的 `curl`。

**版本列表是空的或过时。**
版本索引始终实时抓取；检查网络，并用 `--verbose` 看看是哪个端点失败。

**日志。**
当 `logs.mode` 为 `on-error` 时，只在失败后才往 `logs/` 写日志文件。`--log` 可强制本次运行写日志，`--debug` 会提高日志级别。
