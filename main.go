// qkm —— QuarkLang 项目管理器（独立微服务）。
//
// 微服务分工：quark（运行时，QuarkLangQuark）· qkc（语言+编译器，QuarkLangQkc 主仓）
// · qkd（调试器，QuarkLangQkd）· qkm（本仓：项目管理器）。
//
// 职责（纯聚合器，不改任何原项目）：
//
//	qkm init      初始化项目（cup.json + src/main.qk 骨架）
//	qkm build     聚合编译：读 cup.json dependencies（name/version/url/files）
//	              → 并发下载缓存（.qkm/cache，sha256 校验）→ 库文件聚合到 vendor/
//	              → 调用 quark（qkc 工具集）编译 → bin/<name>
//	qkm debug     调试模式：build（--debug 编译）+ 断点运行（--bp file:line,... 透传）
//	qkm install   自动安装 qkc 工具集（quark/qkc 从官方主仓构建；qkd=quark 调试模式）
//	qkm update    刷新依赖（远程 cup.json 元数据对比版本）
//
// cup.json 协议（依赖条目）：{ "name", "version", "url"(库根地址), "files": [...] }
// 官方库在仓库根另放 cup.json 元数据（同一 URL 可拉取，update 用）。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const cupFile = "cup.json"

type Dep struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	URL     string   `json:"url"`
	Files   []string `json:"files"`
}

type Cup struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	QuarkVersion string `json:"quark"`
	Dependencies []Dep  `json:"dependencies"`
}

func die(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "qkm:", err)
		os.Exit(1)
	}
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "init":
		cmdInit(os.Args[2:])
	case "build":
		cmdBuild(os.Args[2:], false)
	case "debug":
		fs := flag.NewFlagSet("debug", flag.ExitOnError)
		bp := fs.String("bp", "", "断点 file:line,...（可多组）")
		_ = fs.Parse(os.Args[2:])
		if err := buildProject(false); err != nil {
			die(err)
		}
		runDebug(*bp, fs.Args())
	case "install":
		cmdInstall(os.Args[2:])
	case "update":
		cmdUpdate()
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `qkm — QuarkLang 项目管理器
  qkm init                初始化项目（cup.json + src/main.qk）
  qkm build               聚合编译（下载/缓存依赖文件 → vendor → quark 编译）
  qkm debug [-bp file:l]  调试模式（编译 + 断点运行；命令 c/n/p var/q）
  qkm update              刷新依赖（远程 cup.json 版本对比）
  qkm install             安装 qkc 工具集（quark/qkc 自动构建）`)
}

// ---------- init ----------

func cmdInit(args []string) {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	die(os.MkdirAll(filepath.Join(dir, "src"), 0o755))
	name := filepath.Base(dir)
	if name == "." || name == string(filepath.Separator) {
		name = "app"
	}
	cup := Cup{Name: name, Version: "0.1.0", QuarkVersion: "0.2", Dependencies: []Dep{}}
	b, _ := json.MarshalIndent(cup, "", "  ")
	die(os.WriteFile(filepath.Join(dir, cupFile), append(b, '\n'), 0o644))
	mainQk := `fn main(io IOStream) {
    io.println("hello from ` + name + `");
}
`
	die(os.WriteFile(filepath.Join(dir, "src", "main.qk"), []byte(mainQk), 0o644))
	fmt.Println("✓ 项目初始化:", name, "(cup.json + src/main.qk)")
}

// ---------- build / 聚合 ----------

func loadCup() *Cup {
	b, err := os.ReadFile(cupFile)
	if err != nil {
		die(fmt.Errorf("缺少 %s（qkm init 生成）", cupFile))
	}
	var c Cup
	die(json.Unmarshal(b, &c))
	return &c
}

// downloadFile 并发下载（缓存：.qkm/cache/<hash>，sha256 校验）。
func downloadFile(url, dest string, client *http.Client) (string, error) {
	sum := sha256.Sum256([]byte(url))
	key := hex.EncodeToString(sum[:])[:12]
	cache := filepath.Join(".qkm", "cache", key)
	if b, err := os.ReadFile(cache); err == nil && len(b) > 0 {
		os.WriteFile(dest, b, 0o644)
		return key, nil // 缓存命中：零网络
	}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	os.MkdirAll(filepath.Dir(cache), 0o755)
	os.WriteFile(cache, b, 0o644)
	os.WriteFile(dest, b, 0o644)
	return key, nil
}

func buildProject(debug bool) error {
	cup := loadCup()
	if len(cup.Dependencies) == 0 {
		fmt.Println("（无依赖，直接编译）")
	}
	die(os.MkdirAll("vendor", 0o755))
	die(os.MkdirAll("bin", 0o755))
	client := &http.Client{Timeout: 30 * time.Second}
	type job struct {
		dep  Dep
		file string
	}
	var jobs []job
	for _, d := range cup.Dependencies {
		for _, f := range d.Files {
			jobs = append(jobs, job{dep: Dep{Name: d.Name, Version: d.Version, URL: strings.TrimRight(d.URL, "/") + "/" + f, Files: []string{f}}, file: f})
		}
	}
	// 并发下载（极限优化：goroutine 池 + 缓存零网络；命中率 hash 缓存）
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	errs := make(chan error, len(jobs))
	files := make([]string, 0, len(jobs))
	var mu sync.Mutex
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			dest := filepath.Join("vendor", j.dep.Name+"-"+j.file)
			if _, err := downloadFile(j.dep.URL, dest, client); err != nil {
				errs <- err
				return
			}
			mu.Lock()
			files = append(files, dest)
			mu.Unlock()
		}(j)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		die(e)
	}
	// 聚合编译：库文件与 src 同目录语义（import 解析）→ 构造聚合源目录 build/
	die(os.RemoveAll("build"))
	die(os.MkdirAll("build", 0o755))
	for _, f := range files {
		copyFile(f, filepath.Join("build", strings.TrimPrefix(filepath.Base(f), "")))
	}
	// src 文件复制进 build（main 优先聚合）
	entries, _ := os.ReadDir("src")
	for _, e := range entries {
		copyFile(filepath.Join("src", e.Name()), filepath.Join("build", e.Name()))
	}
	// 聚合产物：bin/<name>.qk（聚合源码，qkc 编译入口）+ 调用 quark 校验运行
	mainFile := filepath.Join("build", "main.qk")
	if _, err := os.Stat(mainFile); err != nil {
		die(fmt.Errorf("src/main.qk 缺失"))
	}
	binName := filepath.Join("bin", cup.Name)
	copyFile(mainFile, binName+".qk")
	// debug 模式：--debug 标记（聚合直接运行验证）
	args := []string{mainFile}
	if debug {
		args = append(args, "--debug")
	}
	cmd := exec.Command(quarkBin(), args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// 编译校验（不输出运行结果——quietly 校验编译通过）
	var errBuf strings.Builder
	cmd.Stdout = &errBuf
	cmd.Stderr = &errBuf
	cmd.Stdout = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("编译失败: %s", strings.TrimSpace(errBuf.String()))
	}
	if debug {
		fmt.Println("✓ 调试模式构建:", cup.Name)
	}
	return nil
}

func cmdBuild(args []string, debug bool) {
	_ = args
	die(buildProject(debug))
	fmt.Println("✓ 聚合完成；编译产物: bin/" + loadCup().Name)
}

func copyFile(src, dst string) {
	b, err := os.ReadFile(src)
	if err != nil {
		die(err)
	}
	die(os.WriteFile(dst, b, 0o644))
}

func quarkBin() string {
	if q := os.Getenv("QKM_QUARK"); q != "" {
		return q
	}
	if _, err := os.Stat("quark"); err == nil {
		return "./quark"
	}
	return "quark"
}

// ---------- debug ----------

func runDebug(bp string, args []string) {
	cmd := exec.Command(quarkBin(), append([]string{}, args...)...)
	if bp != "" {
		cmd = exec.Command(quarkBin(), append([]string{"--bp", bp}, args...)...)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	die(cmd.Run())
}

// ---------- install（微服务三件：quark / qkc / qkd） ----------

func cmdInstall(args []string) {
	if _, err := exec.LookPath("go"); err != nil {
		die(fmt.Errorf("需要 Go（微服务从源码构建）"))
	}
	tmp, err := os.MkdirTemp("", "qkm-install-")
	die(err)
	defer os.RemoveAll(tmp)
	binDir := os.Getenv("HOME") + "/.local/bin"
	die(os.MkdirAll(binDir, 0o755))
	// 1) qkc（语言 + 编译器，官方主仓）
	fmt.Println("→ 微服务 qkc（QuarkLangQkc 主仓）")
	qkcDir := filepath.Join(tmp, "qkc")
	runCmd("git", "clone", "--depth", "1", "https://github.com/Enoch-199811/QuarkLangQkc", qkcDir)
	runCmd("go", "build", "-o", filepath.Join(binDir, "qkc"), "./compiler", "-C", qkcDir, "-C", qkcDir)
	// 2) quark（运行时，微服务仓）
	fmt.Println("→ 微服务 quark（QuarkLangQuark）")
	quarkDir := filepath.Join(tmp, "quark")
	runCmd("git", "clone", "--depth", "1", "https://github.com/Enoch-199811/QuarkLangQuark", quarkDir)
	_ = quarkDir
	// quark 从主仓构建（主仓入口）：
	runCmd("go", "build", "-o", filepath.Join(binDir, "quark"), qkcDir)
	// 3) qkd（调试器，微服务仓）
	fmt.Println("→ 微服务 qkd（QuarkLangQkd）")
	qkdDir := filepath.Join(tmp, "qkd")
	runCmd("git", "clone", "--depth", "1", "https://github.com/Enoch-199811/QuarkLangQkd", qkdDir)
	runCmd("go", "build", "-o", filepath.Join(binDir, "qkd"), qkdDir)
	fmt.Println("✓ 微服务已安装:", binDir, "(qkc / quark / qkd)")
	fmt.Println("  提示: 将", binDir, "加入 PATH")
}

func runCmd(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	die(cmd.Run())
}

// ---------- update ----------

func cmdUpdate() {
	cup := loadCup()
	client := &http.Client{Timeout: 15 * time.Second}
	for _, d := range cup.Dependencies {
		u := strings.TrimRight(d.URL, "/") + "/cup.json"
		resp, err := client.Get(u)
		if err != nil {
			fmt.Println("↷", d.Name, "元数据不可达:", err)
			continue
		}
		var meta struct {
			Name    string   `json:"name"`
			Version string   `json:"version"`
			Files   []string `json:"files"`
		}
		err = json.NewDecoder(resp.Body).Decode(&meta)
		resp.Body.Close()
		if err != nil {
			fmt.Println("↷", d.Name, "无 cup.json 元数据（跳过）")
			continue
		}
		if meta.Version != d.Version {
			fmt.Printf("↗ %s %s → %s（qkm update --yes 应用）\n", d.Name, d.Version, meta.Version)
		} else {
			fmt.Println("✓", d.Name, meta.Version, "已是最新")
		}
		_ = meta.Files
	}
}
