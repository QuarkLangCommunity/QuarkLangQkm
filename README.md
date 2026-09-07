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
      "url": "https://raw.githubusercontent.com/Enoch-199811/QuarkLangLibs-Cleg/master",
      "files": ["cleg.qk"]
    }
  ]
}
```

- **files 字段**：库聚合只取列出的文件（不整库拖）——项目管理的核心聚合动作
- url = 库根（GitHub raw / 任意 HTTP 静态地址）
- 官方库在仓库根放 `cup.json`（name/version/files 元数据），`qkm update` 依此做版本对比

## 命令

```sh
qkm init                 # 初始化（cup.json + src/main.qk）
qkm build                # 聚合编译：并发下载（.qkm/cache 哈希缓存，命中零网络）
                         # → vendor/（files 隔离）→ build/ 聚合树 → quark 编译校验 → bin/<name>.qk
qkm debug [-bp 12]       # 调试：构建 + 断点运行（调用 qkd/quark 调试模式；c/n/p var/q）
qkm update               # 刷新依赖版本（远程 cup.json 对比）
qkm install              # 自动安装微服务三件：qkc / quark / qkd（$HOME/.local/bin）
```

## 智能维护 / 极限优化

- 并发下载（8 路 goroutine 池）；缓存按 URL sha256 键控——重复构建零网络
- files 字段精确聚合（最小 vendor）；构建树临时目录化，干净可复现
- update 时逐库拉 cup.json 元数据做版本对比（人工确认再应用）
- 安装脚本幂等（clone --depth 1 + 构建到 binDir）

## 认证信息

- 认证方：QuarkLang 官方项目（QuarkLangQkc）
- 语言版本：QuarkLang v0.2（随主仓 engineVersion）
