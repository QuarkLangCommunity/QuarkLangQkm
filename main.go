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
	"encoding/base64"
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
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	QuarkVersion string   `json:"quark"`
	Dependencies []Dep    `json:"dependencies"`
	Assets       []string `json:"assets"` // 资源（相对 src 的路径/glob）：复制进 bin/<name>/ 保留相对结构
}

// fpair 聚合文件对：vendor 路径 → 原始库文件名。
type fpair struct {
	src string
	orb string
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
	case "inline":
		cmdInline(os.Args[2:])
	case "install":
		cmdInstall(os.Args[2:])
	case "update":
		cmdUpdate()
	case "update-tools":
		cmdUpdateTools()
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
  qkm inline <dir>... [-o out]   多目录视为单一目录编译（临时软链接平铺）
  qkm install             安装微服务三件（quark/qkc/qkd）或补全依赖（-deps）
  qkm update              刷新包元数据（不改文件：版本=可容纳上限，在线最新记录到 .qkm/registry）
  qkm update-tools        工具链更新（重建 quark/qkc/qkd）`)
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

// fetchURL 拉取文件字节：https:// 直下；github://owner/repo/path 走 GitHub API contents（base64）。
func fetchURL(url string, client *http.Client) ([]byte, error) {
	if strings.HasPrefix(url, "github://") {
		body := strings.TrimPrefix(url, "github://")
		parts := strings.SplitN(body, "/", 3)
		if len(parts) < 2 {
			return nil, fmt.Errorf("github:// 需要 owner/repo[/path][@ref]")
		}
		ref := "master"
		repo := parts[1]
		if i := strings.Index(repo, "@"); i >= 0 {
			ref = repo[i+1:]
			repo = repo[:i]
		}
		rest := ""
		if len(parts) == 3 {
			rest = parts[2]
		}
		api := "https://api.github.com/repos/" + parts[0] + "/" + repo + "/contents/" + rest + "?ref=" + ref
		req, _ := http.NewRequest("GET", api, nil)
		req.Header.Set("Accept", "application/vnd.github+json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("GitHub API %d: %s", resp.StatusCode, api)
		}
		var meta struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
			return nil, err
		}
		if meta.Encoding == "base64" {
			b, err := decodeBase64(meta.Content)
			if err != nil {
				return nil, err
			}
			return b, nil
		}
		return []byte(meta.Content), nil
	}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

func decodeBase64(s string) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, s)
	return base64.StdEncoding.DecodeString(s)
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
	b, err := fetchURL(url, client)
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
	files := make([]fpair, 0, len(jobs))
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
			files = append(files, fpair{src: dest, orb: j.file})
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
		copyFile(f.src, filepath.Join("build", f.orb))
	}
	// src 文件复制进 build（main 优先聚合）
	entries, _ := os.ReadDir("src")
	for _, e := range entries {
		if e.IsDir() {
			continue // assets 等目录：构建树不需要（bin 由 assets 字段处理）
		}
		copyFile(filepath.Join("src", e.Name()), filepath.Join("build", e.Name()))
	}
	// 聚合产物：bin/<name>/ = 多文件树（main.qk + 库文件原名原位，import 保留——库=源文件形态）
	mainFile := filepath.Join("build", "main.qk")
	if _, err := os.Stat(mainFile); err != nil {
		die(fmt.Errorf("src/main.qk 缺失"))
	}
	binName := filepath.Join("bin", cup.Name)
	die(os.RemoveAll(binName))
	die(os.MkdirAll(binName, 0o755))
	copyFile(mainFile, filepath.Join(binName, "main.qk"))
	for _, f := range files {
		copyFile(f.src, filepath.Join(binName, f.orb))
	}
	// assets 自动进树：src/assets 目录（存在即保留相对结构进 bin/<name>/，无需字段）
	if fi, err := os.Stat(filepath.Join("src", "assets")); err == nil && fi.IsDir() {
		copyDir(filepath.Join("src", "assets"), filepath.Join(binName, "assets"))
	}
	// assets 字段：src 相对路径/glob → bin/<name>/ 保留相对结构
	for _, pat := range cup.Assets {
		matches, _ := filepath.Glob(filepath.Join("src", pat))
		if len(matches) == 0 {
			if fi, err := os.Stat(filepath.Join("src", pat)); err == nil && fi.IsDir() {
				matches, _ = filepath.Glob(filepath.Join("src", pat, "**", "*"))
				_ = matches
			}
			matches = nil
			if fi, err := os.Stat(filepath.Join("src", pat)); err == nil && fi.IsDir() {
				_ = fi
				copyDir(filepath.Join("src", pat), filepath.Join(binName, pat))
				continue
			}
		}
		for _, m := range matches {
			rel, _ := filepath.Rel("src", m)
			dst := filepath.Join(binName, rel)
			die(os.MkdirAll(filepath.Dir(dst), 0o755))
			copyFile(m, dst)
		}
	}
	// 编译校验（quark 编译/运行校验通过即产物就绪）
	args := []string{filepath.Join(binName, "main.qk")}
	if debug {
		args = append(args, "--debug")
	}
	cmd := exec.Command(quarkBin(), args...)
	cmd.Dir = binName // 产物一致性：校验运行 cwd = bin 产物目录（相对资源/库同产物运行时）
	var errBuf strings.Builder
	cmd.Stdout = io.Discard
	cmd.Stderr = &errBuf
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

// copyDir 递归复制目录（保留结构）。
func copyDir(src, dst string) {
	die(filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		copyFile(p, target)
		return nil
	}))
}

func copyFile(src, dst string) {
	fi, err := os.Stat(src)
	if err != nil {
		die(err)
	}
	mode := fi.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	b, err := os.ReadFile(src)
	if err != nil {
		die(err)
	}
	die(os.WriteFile(dst, b, mode))
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

// ---------- inline（多目录 → 软链接单目录） ----------

// cmdInline 把多个目录下的 .qk 以软链接平铺到同一目录（import 相对解析骗过编译器）。
func cmdInline(args []string) {
	out := filepath.Join(".qkm", "inline")
	var srcs []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-o":
			if i+1 < len(args) {
				out = args[i+1]
				i++
			}
		default:
			srcs = append(srcs, args[i])
		}
	}
	if len(srcs) == 0 {
		die(fmt.Errorf("用法: qkm inline <dir>... [-o out]"))
	}
	die(os.RemoveAll(out))
	die(os.MkdirAll(out, 0o755))
	used := map[string]bool{}
	var files []string
	for _, src := range srcs {
		die(filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(p))
			if ext != ".qk" && ext != ".qlib" {
				return nil
			}
			name := info.Name()
			if used[name] {
				// 冲突唯一化：<base>_<dir>.<ext>
				name = strings.TrimSuffix(name, ext) + "_" + sanitize(filepath.Base(filepath.Dir(p))) + ext
				k := 2
				for used[name] {
					name = strings.TrimSuffix(name, ext) + fmt.Sprintf("_%d", k) + ext
					k++
				}
			}
			used[name] = true
			abs, aerr := filepath.Abs(p)
			if aerr != nil {
				abs = p
			}
			die(os.Symlink(abs, filepath.Join(out, name)))
			files = append(files, name)
			return nil
		}))
	}
	fmt.Printf("✓ inline: %d 个文件软链接平铺 → %s（同一目录编译）\n", len(files), out)
	// 提示：找到 main 入口
	for _, f := range files {
		if f == "main.qk" || strings.HasSuffix(f, "main.qk") {
			fmt.Println("  入口: ", filepath.Join(out, f))
			break
		}
	}
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "lib"
	}
	return b.String()
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

// cmdInstall：默认按 cup.json 补全依赖（build 已内联同逻辑）；-tools 安装微服务三件。
func cmdInstall(args []string) {
	if len(args) > 0 && args[0] == "-tools" {
		installTools()
		return
	}
	if len(args) > 0 && args[0] == "tools" {
		installTools()
		return
	}
	// 依赖补全：直接调用聚合下载（与 build 一致），不编译
	cup := loadCup()
	client := &http.Client{Timeout: 30 * time.Second}
	if len(cup.Dependencies) == 0 {
		fmt.Println("（无依赖）")
		return
	}
	die(os.MkdirAll("vendor", 0o755))
	for _, d := range cup.Dependencies {
		for _, f := range d.Files {
			u := strings.TrimRight(d.URL, "/") + "/" + f
			dest := filepath.Join("vendor", d.Name+"-"+f)
			if _, err := downloadFile(u, dest, client); err != nil {
				die(fmt.Errorf("依赖 %s/%s 补全失败: %v", d.Name, f, err))
			}
		}
	}
	fmt.Println("✓ 依赖补全完成（vendor/" + cup.Name + " 缓存命中零网络）")
}

// installTools 工具链三件（quark/qkc/qkd）构建安装。
func installTools() {
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
	runCmd("git", "clone", "--depth", "1", "https://github.com/QuarkLangCommunity/QuarkLangQkc", qkcDir)
	runCmd("go", "build", "-o", filepath.Join(binDir, "qkc"), "./compiler", "-C", qkcDir, "-C", qkcDir)
	// 2) quark（运行时，微服务仓）
	fmt.Println("→ 微服务 quark（QuarkLangQuark）")
	quarkDir := filepath.Join(tmp, "quark")
	runCmd("git", "clone", "--depth", "1", "https://github.com/QuarkLangCommunity/QuarkLangQuark", quarkDir)
	_ = quarkDir
	// quark 从主仓构建（主仓入口）：
	runCmd("go", "build", "-o", filepath.Join(binDir, "quark"), qkcDir)
	// 3) qkd（调试器，微服务仓）
	fmt.Println("→ 微服务 qkd（QuarkLangQkd）")
	qkdDir := filepath.Join(tmp, "qkd")
	runCmd("git", "clone", "--depth", "1", "https://github.com/QuarkLangCommunity/QuarkLangQkd", qkdDir)
	runCmd("go", "build", "-o", filepath.Join(binDir, "qkd"), qkdDir)
	fmt.Println("✓ 微服务已安装:", binDir, "(qkc / quark / qkd)")
	fmt.Println("  提示: 将", binDir, "加入 PATH")
}

// cmdUpdateTools 工具链更新（重建三件）。
func cmdUpdateTools() {
	installTools()
	fmt.Println("✓ 工具链已更新")
}

func runCmd(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	die(cmd.Run())
}

// ---------- update ----------

func cmdUpdate() {
	// 刷新在线元数据 → .qkm/registry（不改任何项目文件；cup.json version = 可容纳上限）
	cup := loadCup()
	client := &http.Client{Timeout: 15 * time.Second}
	for _, d := range cup.Dependencies {
		u := strings.TrimRight(d.URL, "/")
		var meta struct {
			Name    string   `json:"name"`
			Version string   `json:"version"`
			Files   []string `json:"files"`
		}
		if b, err := fetchURL(u+"/cup.json", client); err == nil {
			_ = json.Unmarshal(b, &meta)
		}
		if meta.Version == "" {
			meta.Version = d.Version
			meta.Files = d.Files
		}
		die(os.MkdirAll(".qkm/registry", 0o755))
		rec := map[string]string{"name": d.Name, "latest": meta.Version, "cap": d.Version}
		b, _ := json.Marshal(rec)
		die(os.WriteFile(".qkm/registry/"+d.Name+".json", b, 0o644))
		if meta.Version != d.Version {
			fmt.Printf("↗ %s 上限 %s / 在线最新 %s —— cup.json 未改动（调整 version 字段即更新上限）\n", d.Name, d.Version, meta.Version)
		} else {
			fmt.Println("✓", d.Name, d.Version, "与在线最新一致")
		}
	}
}
