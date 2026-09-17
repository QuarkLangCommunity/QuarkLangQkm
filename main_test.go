package main

// qkm 单元测试：镜像地址映射、离线模式、缓存命中、参数拆分、工具定位。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMirrorURL(t *testing.T) {
	cases := []struct {
		name   string
		mirror string
		in     string
		want   string
	}{
		{"带 ref（repo@ref 形式）", "https://m.example.com", "github://org/repo@v1.2/lib/x.qk", "https://m.example.com/org/repo/v1.2/lib/x.qk"},
		{"仅仓库带 ref", "https://m.example.com", "github://org/repo@dev", "https://m.example.com/org/repo/dev/"},
		{"无 ref 默认 master", "https://m.example.com", "github://org/repo/lib/x.qk", "https://m.example.com/org/repo/master/lib/x.qk"},
		{"镜像末尾斜杠归一", "https://m.example.com/", "github://org/repo/x.qk", "https://m.example.com/org/repo/master/x.qk"},
		{"无镜像配置", "", "github://org/repo/x.qk", ""},
		{"非 github 地址不映射", "https://m.example.com", "https://raw.example.com/x.qk", ""},
		{"仓库级（无路径）", "https://m.example.com", "github://org/repo", "https://m.example.com/org/repo/master/"},
		{"非法 github 地址", "https://m.example.com", "github://org", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("QKM_MIRROR", c.mirror)
			if got := mirrorURL(c.in); got != c.want {
				t.Errorf("mirrorURL(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestOfflineMode(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Setenv("QKM_OFFLINE", v)
		if !offlineMode() {
			t.Errorf("QKM_OFFLINE=%q 应判定为离线", v)
		}
	}
	for _, v := range []string{"", "0", "false", "no"} {
		t.Setenv("QKM_OFFLINE", v)
		if offlineMode() {
			t.Errorf("QKM_OFFLINE=%q 不应判定为离线", v)
		}
	}
}

// 离线 + 缓存：命中缓存必须零网络成功；未命中必须报错且不提网络请求。
func TestDownloadFileOfflineCache(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	url := "github://org/repo/lib.qk@v1"
	dest := filepath.Join(dir, "out.qk")

	t.Setenv("QKM_OFFLINE", "1")
	if _, err := downloadFile(url, dest, newHTTPClient(0)); err == nil {
		t.Fatal("离线且无缓存时应当报错")
	} else if !strings.Contains(err.Error(), "离线") {
		t.Errorf("错误信息应说明离线：%v", err)
	}

	// 手工写入缓存（键 = sha256(url)[:12]），应命中且零网络
	key := cacheKeyFor(url)
	cacheDir := filepath.Join(dir, ".qkm", "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, key), []byte("fn x() void {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := downloadFile(url, dest, newHTTPClient(0)); err != nil {
		t.Fatalf("命中缓存应成功：%v", err)
	}
	b, err := os.ReadFile(dest)
	if err != nil || string(b) != "fn x() void {}\n" {
		t.Errorf("产物内容不对: %q err=%v", b, err)
	}
}

func TestSplitArgs(t *testing.T) {
	flags, targets := splitArgs([]string{"-v", "-run", "正则", "-j", "4", "tests", "src/a.qk"},
		map[string]bool{"-run": true, "-j": true, "-L": true})
	wantFlags := []string{"-v", "-run", "正则", "-j", "4"}
	if strings.Join(flags, " ") != strings.Join(wantFlags, " ") {
		t.Errorf("flags = %v, want %v", flags, wantFlags)
	}
	if strings.Join(targets, " ") != "tests src/a.qk" {
		t.Errorf("targets = %v", targets)
	}
	if !hasFlag(flags, "-run") || hasFlag(flags, "-L") {
		t.Errorf("hasFlag 判定不对: %v", flags)
	}
}

func TestQkFilesUnder(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "src", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.qk", "deep/b.qk", "note.txt"} {
		p := filepath.Join(dir, "src", f)
		if err := os.WriteFile(p, []byte("fn f() void {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := qkFilesUnder(filepath.Join(dir, "src"))
	if len(got) != 2 {
		t.Fatalf("应收集 2 个 .qk，got %v", got)
	}
	if !strings.HasSuffix(got[0], "a.qk") || !strings.HasSuffix(got[1], "b.qk") {
		t.Errorf("结果应有序且只含 .qk: %v", got)
	}
	// 单文件目标原样返回
	if one := qkFilesUnder(filepath.Join(dir, "src", "a.qk")); len(one) != 1 {
		t.Errorf("单文件目标应返回自身: %v", one)
	}
}

func TestToolBinPrefersEnv(t *testing.T) {
	t.Setenv("QKM_QKFMT", "/opt/custom/qkfmt")
	if got := toolBin("qkfmt", "QKM_QKFMT"); got != "/opt/custom/qkfmt" {
		t.Errorf("应优先环境变量，got %q", got)
	}
	t.Setenv("QKM_QKFMT", "")
	if got := toolBin("qkfmt", "QKM_QKFMT"); got != "qkfmt" {
		t.Errorf("无本地文件时应回落到 PATH 名字，got %q", got)
	}
}
