# QuarkLangQkm

**qkm —— QuarkLang 项目管理器（独立微服务）。**

微服务分工：quark（运行时，QuarkLangQuark）· qkc（语言+编译器，QuarkLangQkc 主仓）· qkd（调试器，QuarkLangQkd）· **qkm**（本仓：项目管理/聚合）。

## cup.json（依赖协议）

```json
{
  "name": "myapp",
  "version": "0.1.0",
  "quark": "0.2",
  "dependencies": [
    {
      "name": "cleg",
      "version": "1.0.0",
      "url": "github://QuarkLangCommunity/QuarkLangLibs-Cleg@master",
      "files": ["cleg.qk"]
    }
  ]
}
```

- **files 字段**：库聚合只取列出的文件（不整库拖）——项目管理的核心聚合动作
- url = 库根；官方协议 `github://owner/repo@ref`（qkm 经 GitHub API contents 拉取，避免 raw 域名网络差异）；`https://` 任意 HTTP 静态直下也支持
- 官方库在仓库根放 `cup.json`（name/version/files 元数据），`qkm update` 依此做版本对比

## 命令

```sh
qkm init [-t 模板] [-list] [目录]   # 初始化（cup.json + 模板文件；模板：app/cli/lib/gui/test）
                         #   模板里文件名与内容的 {{name}} 替换为项目名；模板自带 cup.json 时以模板为准（可预置依赖）
                         #   QKM_TEMPLATES=<dir> 用磁盘上的自定义/镜像模板
qkm build                # 聚合编译（cup.json `assets` 字段：src 相对路径/glob 任意文件随 bin/<name>/ 保留相对结构——
                         #   .so 动态库 / png / txt / 任意类型不限，权限随源文件）：并发下载（.qkm/cache 哈希缓存，命中零网络）
                         # → vendor/（files 隔离）→ build/ 聚合树 → quark 编译校验 → bin/<name>/（多文件产物：
                         #   main.qk + 库文件原名原位，import 保留；cd bin/<name> && quark main.qk 即运行）
qkm debug [-bp 12]       # 调试：构建 + 断点运行（调用 qkd/quark 调试模式；c/n/p var/q）
qkm run [--native] [args...]   # 构建并运行（默认解释器；--native 走 qkc -run；--no-build 跳过构建）
                         #   首个 -- 只是分隔符：qkm run -- alice --shout → 程序收到 alice --shout
qkm fmt [-w|-l|-d] [路径...]   # 格式化（默认 src/；-w 原地写 / -l 只列出（CI）/ -d 看差异）
qkm test [-v] [-run 正则] [-j N] [-L 目录] [目标...]   # 跑测试（默认 tests/；未给 -L 时自动加 src/ 与 build/，
                         #   后者是聚合树：cup.json 依赖以原文件名落在这里，import 无需手动 -L）
qkm inline <dir>...     # 多目录视为单一目录编译：临时软链接平铺（.qkm/inline，-o 指定）
                         # （同名冲突自动唯一化；import 相对同目录正常解析）
qkm update               # 刷新依赖版本（远程 cup.json 对比）
qkm install              # 自动安装微服务三件：qkc / quark / qkd（$HOME/.local/bin）
```

### 项目模板（qkm init）

模板内嵌在二进制里（离线可用），`qkm init -list` 列出：

| 模板 | 说明 | 初始化后的内容 |
|---|---|---|
| `app` | 最小可运行项目（默认） | `cup.json` + `src/main.qk` |
| `cli` | 命令行骨架 | `main(IOStream io, HashTable<String,String> env, List<String> args)` + 参数解析 |
| `lib` | 库骨架 | `src/{{name}}.qk`（space + struct/impl）、`src/main.qk` 演示入口、`tests/`（qkt）、cup.json 预置 qkt 依赖 |
| `gui` | GUI 骨架 | `src/main.qk`（style 冒烟）、cup.json 预置 cleg + style 依赖 |
| `test` | 被测代码 + 测试 | `src/calc.qk`、`src/main.qk`、`tests/smoke_test.qk` |

```sh
qkm init -t lib mylib     # mylib/src/mylib.qk、mylib/tests/mylib_test.qk（占位符全部替换）
qkm init -t gui window    # cup.json 已带 cleg/style 依赖 → qkm build 直接拉取聚合
QKM_TEMPLATES=~/my-tpls qkm init -t mine proj   # 自定义模板目录（离线/镜像）
```

每个模板的实际用法写在生成项目的 `README.md` 里；模板目录结构与占位符约定见 [`templates/README.md`](templates/README.md)。

### 工具定位（三条命令共用）

`$QKM_QUARK` / `$QKM_QKFMT` / `$QKM_QKTEST` / `$QKM_QKC` → 当前目录下的同名文件 → `PATH`。
工具缺失时给出构建提示（主仓 `go build -o qkfmt ./cmd/qkfmt`）。

```sh
# 典型用法
qkm fmt -l                 # CI 门禁：有未格式化文件退出 1
qkm test -v -j 4           # 并发跑 tests/
qkm run --native arg1 arg2 # 编译执行（qkc -run），参数透传给程序
```

### 依赖获取：镜像与离线

| 环境变量 | 作用 |
|---|---|
| `QKM_MIRROR=https://mirror.example.com` | 镜像前缀：`github://owner/repo@ref/path` → `<mirror>/owner/repo/<ref>/path`；镜像失败自动回落 GitHub |
| `QKM_OFFLINE=1` | 只用 `.qkm/cache`，**绝不发起网络请求**；未命中缓存时报错并给出缓存键；`install -tools` / `update` 跳过网络 |

- 缓存键 = 原始 URL 的 sha256 前 12 位（**与镜像无关**）：换镜像仍命中同一份缓存，镜像仅影响首次获取。
- 实测：本地 HTTP 镜像可完整拉取依赖（`qkm install` → `vendor/`）；关掉镜像进程后
  `QKM_OFFLINE=1 qkm build` 仍从缓存成功构建，清缓存后报
  `离线模式（QKM_OFFLINE=1）：未命中缓存 github://…（缓存键 …）`。

## 实测（端到端）

```sh
$ qkm init demo && cd demo
$ qkm build                      # ✓ 聚合完成；编译产物: bin/demo
$ qkm run hello world            # hello from demo
$ qkm test -L ../QuarkLangQktest # PASS demo_test (2ms) / qktest: 1 passed, 0 failed
$ qkm fmt -l                     # 有未格式化文件 → 退出 1；qkm fmt -w 后 → 退出 0
$ qkm run --native --no-build    # 编译路径（qkc + clang）：hello from demo
```

## 智能维护 / 极限优化

- 并发下载（8 路 goroutine 池）；缓存按 URL sha256 键控——重复构建零网络
- files 字段精确聚合（最小 vendor）；构建树临时目录化，干净可复现
- update 时逐库拉 cup.json 元数据做版本对比（人工确认再应用）
- 安装脚本幂等（clone --depth 1 + 构建到 binDir）

## 认证信息

- 认证方：QuarkLang 官方项目（QuarkLangQkc）
- 语言版本：QuarkLang v0.2（随主仓 engineVersion）
