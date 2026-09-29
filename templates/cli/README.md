# {{name}}

命令行项目骨架：`main(IOStream io, HashTable<String, String> env, List<String> args)`（env/args 形参可选、位置固定）。

```sh
qkm run -- alice --shout      # qkm run 后面的参数透传给程序
qkm run --native -- alice     # 走 qkc 原生编译路径
```
