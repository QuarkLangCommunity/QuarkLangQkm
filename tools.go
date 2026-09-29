package main

// 工具链接入：qkm fmt / test / run —— 薄封装，调用官方工具二进制
// （qkfmt / qktest / quark / qkc），定位规则与 quarkBin 一致：env → ./name → PATH。

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// toolBin 定位工具二进制：$<env> → ./<name> → PATH。
func toolBin(name, env string) string {
	if p := os.Getenv(env); p != "" {
		return p
	}
	if _, err := os.Stat(name); err == nil {
		return "./" + name
	}
	return name
}

// defaultTargets 选默认目录：preferred 存在就用它，否则当前目录。
func defaultTargets(preferred string) []string {
	if fi, err := os.Stat(preferred); err == nil && fi.IsDir() {
		return []string{preferred}
	}
	return []string{"."}
}

// qkFilesUnder 收集目录下（递归）或单个路径的 .qk 文件。
func qkFilesUnder(target string) []string {
	fi, err := os.Stat(target)
	if err != nil || !fi.IsDir() {
		return []string{target}
	}
	var out []string
	_ = filepath.Walk(target, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".qk") {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// splitArgs 把参数分成「透传标志」与「目标」：标志及其取值原样保留。
// valueFlags 列出需要吃掉一个取值的标志（如 -L dir）。
func splitArgs(args []string, valueFlags map[string]bool) (flags, targets []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if valueFlags[a] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		targets = append(targets, a)
	}
	return flags, targets
}

func hasFlag(flags []string, name string) bool {
	for _, f := range flags {
		if f == name {
			return true
		}
	}
	return false
}

// toolExists 判断工具是否可用：带路径的看文件，裸名字看 PATH。
func toolExists(bin string) bool {
	if strings.ContainsAny(bin, `/\`) {
		_, err := os.Stat(bin)
		return err == nil
	}
	_, err := exec.LookPath(bin)
	return err == nil
}

// runTool 执行工具并透传 stdio；返回退出码（工具未找到时给出构建提示）。
func runTool(bin string, args []string, dir string) int {
	if !toolExists(bin) {
		fmt.Fprintf(os.Stderr, "qkm: 找不到工具 %s\n"+
			"  构建（QuarkLangQkc 主仓）：go build -o %s ./cmd/%s\n"+
			"  或指定路径：环境变量（QKM_QKFMT / QKM_QKTEST / QKM_QUARK / QKM_QKC）\n",
			bin, bin, filepath.Base(bin))
		return 127
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "qkm:", err)
		return 1
	}
	return 0
}

// ---------- qkm fmt ----------

// cmdFmt 格式化项目 .qk（默认 src/；-w 原地写 / -l 只列出 / -d 打印差异）。
func cmdFmt(args []string) {
	flags, targets := splitArgs(args, map[string]bool{})
	if len(targets) == 0 {
		targets = defaultTargets("src")
	}
	var files []string
	for _, t := range targets {
		files = append(files, qkFilesUnder(t)...)
	}
	if len(files) == 0 {
		die(fmt.Errorf("没有可格式化的 .qk 文件（目标：%s）", strings.Join(targets, ", ")))
	}
	os.Exit(runTool(toolBin("qkfmt", "QKM_QKFMT"), append(flags, files...), ""))
}

// ---------- qkm test ----------

// cmdTest 跑测试（默认 tests/，没则当前目录）；未显式给 -L 时自动加 -L src。
func cmdTest(args []string) {
	flags, targets := splitArgs(args, map[string]bool{"-L": true, "-run": true, "-j": true})
	if !hasFlag(flags, "-L") {
		// 默认搜索路径：src（项目自身模块）+ build（聚合树：cup.json 依赖用原文件名落在这里，
		// 例如 qkt.qk）——这样 qkm build 之后 qkm test 不用手动 -L。
		for _, d := range []string{"src", "build"} {
			if fi, err := os.Stat(d); err == nil && fi.IsDir() {
				flags = append(flags, "-L", d)
			}
		}
	}
	if len(targets) == 0 {
		targets = defaultTargets("tests")
	}
	env := os.Environ()
	if os.Getenv("QUARK") == "" {
		q := quarkBin()
		if abs, err := filepath.Abs(q); err == nil {
			q = abs
		}
		env = append(env, "QUARK="+q)
	}
	cmd := exec.Command(toolBin("qktest", "QKM_QKTEST"), append(flags, targets...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		die(err)
	}
}

// ---------- qkm run ----------

// cmdRun 运行项目：默认先构建，再以产物目录为 cwd 执行（解释器；--native 走 qkc -run）。
func cmdRun(args []string) {
	native, noBuild := false, false
	var pass []string
	for _, a := range args {
		switch a {
		case "--native", "-n":
			native = true
		case "--no-build":
			noBuild = true
		default:
			pass = append(pass, a)
		}
	}
	// 约定：首个 -- 只是分隔符（qkm run -- alice），透传给程序时丢掉
	if len(pass) > 0 && pass[0] == "--" {
		pass = pass[1:]
	}
	cup := loadCup()
	binDir := filepath.Join("bin", cup.Name)
	if !noBuild {
		die(buildProject(false))
	}
	if _, err := os.Stat(filepath.Join(binDir, "main.qk")); err != nil {
		die(fmt.Errorf("产物不存在：%s（先执行 qkm build）", binDir))
	}
	var bin string
	var argv []string
	if native {
		bin, argv = toolBin("qkc", "QKM_QKC"), append([]string{"-run", "main.qk"}, pass...)
	} else {
		bin, argv = quarkBin(), append([]string{"main.qk"}, pass...)
	}
	os.Exit(runTool(bin, argv, binDir))
}
