# 项目模板

每个子目录是一个模板，`qkm init -t <名字>` 展开到项目目录。

- 文件名与内容里的 `{{name}}` 会替换成项目名
- 模板自带 `cup.json` 时以模板为准（可预置依赖），否则生成默认的 cup.json
- `QKM_TEMPLATES=<dir>` 可换成磁盘上的自定义模板目录（离线/镜像）

| 模板 | 说明 |
|---|---|
| app | 最小可运行项目 |
| cli | 命令行骨架（env/args） |
| lib | 库骨架（space + struct/impl + qkt 测试） |
| gui | GUI 骨架（cleg + style 依赖） |
| test | 被测代码 + tests/ |
