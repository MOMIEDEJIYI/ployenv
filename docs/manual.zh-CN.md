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
| `maven` | `mvn` | `mvn`、`mvnDebug` |
| `adb` | `platform-tools` | `adb`、`fastboot` |

每个运行时都装在 `<数据目录>/versions/<名称>/<版本>/` 下，各自独立。
ployenv 从不做系统级安装，也从不改写不属于自己的文件。

**Maven 是工具，不是语言。** ployenv 只把 `<版本>/bin` 加入 `PATH`，**不设
`MAVEN_HOME`/`M2_HOME`**（`mvn.cmd` 自己推导主目录），也**不碰 `settings.xml`
或 `~/.m2`**。Maven 需要一个 JDK：用 ployenv 装的 `java`，或继续用你自己的
`JAVA_HOME`，两种都可以；两者都不可用时，`ployenv doctor` 会在 stderr 给一行提示。

**adb 也是工具，不是语言。** 它管理 Android platform-tools（`adb` 与 `fastboot`
所在地）。激活某个版本只把该版本的目录加入 `PATH` —— platform-tools 是独立的
SDK 组件，ployenv **从不设 `ANDROID_HOME`/`ANDROID_SDK_ROOT`**，也**不碰
`~/.android`**（那是 adb 自己的目录）。安装时按 SDK 仓库公布的 **SHA-1** 校验；
索引里已不再携带的版本会在**告警后继续**。安装器读 `source.properties` 判断
版本、**不执行 adb**，所以安装过程不会创建 `~/.android`；第一次真正运行 `adb`
时才创建。

版本写法有三种：

| 写法 | 含义 | 示例 |
|---|---|---|
| 精确版本 | 完整版本号 | `ployenv install node@20.20.2` |
| 部分版本 | 取匹配到的最高版本 | `ployenv install node@20` → `20.20.2` |
| 通道 | 联网查询版本索引解析 | `ployenv install node@lts`、`node@latest` |

Maven 的通道只走 3.x 线：`latest` / `stable` / 不带过滤取**最高的稳定 3.x**（当前 3.10.0）；
上游 `<release>` 还指向 4.0 预发布版时 `@release` 会被拒绝；`maven@lts` 会明确报
“没有 LTS 通道”；`ployenv available maven all` 会列出预发布版并标 `preview`。

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

## 与 DSH 插件共用一份 ployenv

装了 DSH 插件 `dsh-plugin-ployenv` 时，它默认**自带**一份 ployenv，数据放在插件包内
（`<插件包>/data/ployenv`），因此卸载/升级插件会带走里面装好的运行时。如果你已经自己装了
ployenv（例如 `C:\ployenv`），可以让插件和命令行**共用同一份程序与数据**：

1. 设置**用户**环境变量，让插件用你的二进制：

   ```text
   PLOYENV_BIN = C:\ployenv\ployenv.exe
   ```

2. 让数据根落在同一个目录（二选一即可）：

   - **不设** `PLOYENV_ROOT`：CLI 默认用**可执行文件旁的目录**（这里就是 `C:\ployenv`）；
   - 或显式设置 `PLOYENV_ROOT = C:\ployenv`。

3. 想让命令行直接敲 `ployenv`（可选）：把 `C:\ployenv` 加进用户 `PATH`。

4. **完全退出并重新打开 DSH**（环境变量在进程启动时读入；设完变量后新开的程序才能看到）。

5. 在 DSH 里验证 `/ployenv status`，应显示：

   ```text
   bin:        C:\ployenv\ployenv.exe
   bin source: PLOYENV_BIN
   root:       C:\ployenv
   ```

之后终端与 DSH 共用同一批运行时；**卸载或升级插件都不会影响 `C:\ployenv` 里的数据**。

> 想改回插件自带的那份：删掉 `PLOYENV_BIN`（和 `PLOYENV_ROOT`）再重启 DSH。
> 解析优先级 —— 二进制：行配置 `bin` > `PLOYENV_BIN` > `PLOYENV_ROOT\ployenv.exe` >
> setup 自装件 > 包内自带 > PATH；数据根：行配置 `root` > `PLOYENV_ROOT` > 包内 `data/ployenv`。

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
| `ployenv use <语言>@<版本> -p` | 写入当前目录的 `.ployenv`（`-p` 是 `--project` 的短写法）。 |
| `ployenv use <语言>@none [-p]` | 取消该默认值 / 删除 `.ployenv` 里那一条；`off`、`default` 同义。 |

`-p`/`--project` 选当前目录的项目文件，`-g`/`--global` 显式声明全局默认。命令不接受的
flag 会被**拒绝**而不是忽略 —— 因此 `--project` 再也不会被用错命令时悄悄改掉全局默认值。

`use` 只是记录版本。要让它在某个终端里生效，需要激活该会话（见下）。

### 会话激活

| 命令 | 说明 |
|---|---|
| `ployenv shell [--shell pwsh\|cmd\|bash]` | 在**同一窗口**打开一个已应用环境的子 shell，窗口标题设为 `[ployenv] <语言>@<版本> - <目录>`（不显示控制台标题的终端看不到）。子 shell 有**独立的命令历史**；`exit` 回到父 shell 后历史与环境都在。 |
| `ployenv env [--shell pwsh\|cmd\|bash\|json]` | 打印一段应用环境的脚本，由你在当前 shell 里 eval。 |
| `ployenv env --json` | 以 JSON 输出同样的信息。 |

对 `maven` 来说，脚本只在 `PATH` 前插入 `<版本>/bin`，不涉及 `MAVEN_HOME`、
`M2_HOME` 或任何 Maven 配置。

对 `adb` 来说，脚本只在 `PATH` 前插入 `<版本>`（platform-tools 是平铺布局），
不涉及 `ANDROID_HOME` 或 `ANDROID_SDK_ROOT`。

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
:: cmd 没有 eval，请改为打开一个已激活的子 shell（以下示例均在 cmd 中执行，不是 PowerShell）
ployenv shell
```

`ployenv shell` 开的是**子 shell**，命令历史从空开始。如果想在**当前 cmd 窗口**里应用环境
（保留窗口已有的历史）：

```bat
:: 这两行要在 cmd.exe 里执行；PowerShell 的 > 会写成 UTF-16LE，cmd 跑不了
ployenv env --shell cmd > %TEMP%\ployenv-env.bat && call %TEMP%\ployenv-env.bat
```

这样历史保留，但离开窗口时环境不会自动还原。

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

`config.json` 就是普通 JSON，可以手动编辑。读取时容忍 UTF-8 BOM（Windows 编辑器常加），写回时规整为不带 BOM 的 UTF-8；无法解析的文件会**告警一次且不会被覆盖**。

```powershell
ployenv config --set-default node 22.23.3    # 设置全局默认版本
ployenv config --set-default node none       # 取消它（也可写 off / default）
```

## 镜像

- 默认使用官方源。
- 配置了镜像则优先使用；失败时回退官方源，最后还可回退到系统 `curl`。
- 版本列表与下载可能使用不同端点：Java / Python 通过官方 API 取版本列表，而下载可以走镜像。Maven 的版本列表走仓库形态基址（默认 Central），下载仍镜像优先；只提供分发包的镜像也能用于下载。
- ployenv 绝不修改系统的 npm / pip 配置或环境变量。

布局要求：

| 运行时 | 期望的镜像基址 |
|---|---|
| `node` | Node.js dist 镜像，`<基址>/v<版本>/<文件>`。 |
| `go` | Go 下载镜像，`<基址>/<文件>`。 |
| `java` | Adoptium 风格的镜像，`<基址>/<主版本>/jdk/<arch>/<os>/<文件>`。 |
| `python` | Windows：NuGet flat-container 基址。其他平台：GitHub Release 镜像，`<基址>/<tag>/<文件>`。 |
| `maven` | **仓库形态**的基址（与 Central 同布局），`<基址>/<版本>/apache-maven-<版本>-bin.<ext>`。Apache 分发镜像（dlcdn、TUNA、阿里云、USTC）有安装包但**没有 `maven-metadata.xml`**：列版本时先试配置的基址一次、再走 Central；下载仍按"镜像优先"顺序。 |
| `adb` | Google SDK 仓库形态的镜像：`<基址>/repository2-3.xml` 加它声明的 `platform-tools_r<版本>-<win\|darwin\|linux>.zip`。腾讯 `https://mirrors.cloud.tencent.com/AndroidSDK` 同字节。 |

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
