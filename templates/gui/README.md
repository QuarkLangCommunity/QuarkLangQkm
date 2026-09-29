# {{name}}

GUI 项目骨架（cleg + style 依赖已写进 `cup.json`）。

```sh
qkm build      # 拉取 cleg.qk / style.qk 到 vendor/ 并聚合编译
qkm run        # 运行 src/main.qk（当前是无窗口的样式引擎冒烟）
```

出窗口的完整例子见 QuarkLangLibs-Cleg/examples；`style.qk` 是通用样式引擎（QSS 颗粒度 + 级联/状态/选择器 + 动画）。
