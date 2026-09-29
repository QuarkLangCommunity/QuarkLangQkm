# {{name}}

库项目：`src/{{name}}.qk` 提供 `{{name}}::` 空间函数与 `Counter` 结构体；`src/main.qk` 是演示入口。

```sh
qkm build            # 拉取 qkt.qk（cup.json 依赖）到 vendor/ 并聚合编译 → bin/{{name}}/
qkm run              # 跑演示入口（bin/{{name}}/main.qk）
qkm test             # 跑 tests/（默认搜索路径 src + build，依赖 qkt 从聚合树找）
```

被其他项目引用：在对方的 `cup.json` 里加一条依赖，`url` 指向本仓库、`files` 写库文件。
