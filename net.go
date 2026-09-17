package main

// 依赖获取的镜像与离线支持（零第三方依赖）。
//
// 环境变量：
//
//	QKM_MIRROR=https://mirror.example.com   镜像前缀：github://owner/repo/path@ref
//	                                          → <mirror>/owner/repo/<ref>/path
//	QKM_OFFLINE=1                            离线：只用 .qkm/cache，绝不发起网络请求
//
// 缓存键是**原始 URL** 的 sha256（与镜像无关），因此同一依赖换镜像仍命中同一份缓存。

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"time"
)

func offlineMode() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("QKM_OFFLINE"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func mirrorBase() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("QKM_MIRROR")), "/")
}

// mirrorURL 把 github:// 依赖地址映射为镜像地址；非 github:// 或未配置镜像时返回 ""。
//
//	github://owner/repo/path/file.qk@v1.2  →  <mirror>/owner/repo/v1.2/path/file.qk
//	github://owner/repo/path/file.qk      →  <mirror>/owner/repo/master/path/file.qk
func mirrorURL(raw string) string {
	base := mirrorBase()
	if base == "" || !strings.HasPrefix(raw, "github://") {
		return ""
	}
	body := strings.TrimPrefix(raw, "github://")
	parts := strings.SplitN(body, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
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
	if repo == "" || ref == "" {
		return ""
	}
	return base + "/" + parts[0] + "/" + repo + "/" + ref + "/" + rest
}

// newHTTPClient 统一的 HTTP 客户端（超时可调）。
func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

// cacheKeyFor 依赖缓存键：URL 的 sha256 前 12 位。
// 注意键只取决于**原始 URL**（与镜像配置无关），换镜像仍命中同一份缓存。
func cacheKeyFor(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:])[:12]
}
