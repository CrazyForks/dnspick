// Package updater 实现从 GitHub Releases 检查并原地更新 dnspick 自身。
package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"time"

	"github.com/minio/selfupdate"
	"golang.org/x/mod/semver"
)

const (
	owner = "palemoky"
	repo  = "dnspick"
	app   = "dnspick"
)

// release 是 GitHub API 返回的 release 信息的子集。
type release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// CheckResult 描述一次更新检查的结论。
type CheckResult struct {
	Current   string // 当前版本（可能是 "dev"）
	Latest    string // 最新发布版本，例如 v2.1.0
	HasUpdate bool   // 是否存在可用的更新
	URL       string // 最新 release 页面地址
}

// Check 查询 GitHub 上最新的 release 并与 current 比较。
func Check(ctx context.Context, current string) (*CheckResult, error) {
	rel, err := latestRelease(ctx)
	if err != nil {
		return nil, err
	}
	return &CheckResult{
		Current:   current,
		Latest:    rel.TagName,
		HasUpdate: isNewer(current, rel.TagName),
		URL:       rel.HTMLURL,
	}, nil
}

// Update 检查并在有新版本时原地替换当前可执行文件。
// 返回更新到的版本号；若已是最新则 updated=false。
func Update(ctx context.Context, current string) (latest string, updated bool, err error) {
	res, err := Check(ctx, current)
	if err != nil {
		return "", false, err
	}
	if !res.HasUpdate {
		return res.Latest, false, nil
	}

	bin, err := downloadBinary(ctx, res.Latest)
	if err != nil {
		return res.Latest, false, err
	}
	if err := selfupdate.Apply(bytes.NewReader(bin), selfupdate.Options{}); err != nil {
		// 失败时尝试回滚到原文件。
		if rerr := selfupdate.RollbackError(err); rerr != nil {
			return res.Latest, false, fmt.Errorf("更新失败且回滚失败: %w", rerr)
		}
		return res.Latest, false, fmt.Errorf("更新失败（已回滚）: %w", err)
	}
	return res.Latest, true, nil
}

// isNewer 判断 latest 是否比 current 更新。
// current 非合法 semver（如 "dev" 或脏构建）时一律视为可更新。
func isNewer(current, latest string) bool {
	if !semver.IsValid(current) {
		return true
	}
	return semver.Compare(current, latest) < 0
}

func latestRelease(ctx context.Context) (*release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("查询最新版本失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("查询最新版本失败: GitHub 返回 %s", resp.Status)
	}

	var rel release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("解析版本信息失败: %w", err)
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("未找到任何发布版本")
	}
	return &rel, nil
}

// downloadBinary 下载指定版本对应当前平台的归档，并解出其中的可执行文件。
func downloadBinary(ctx context.Context, tag string) ([]byte, error) {
	base := fmt.Sprintf("%s-%s-%s", app, runtime.GOOS, runtime.GOARCH)
	isZip := runtime.GOOS == "windows"
	asset := base + ".tar.gz"
	if isZip {
		asset = base + ".zip"
	}
	url := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", owner, repo, tag, asset)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载更新包失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载更新包失败: %s 返回 %s", asset, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取更新包失败: %w", err)
	}

	if isZip {
		return binaryFromZip(data)
	}
	return binaryFromTarGz(data)
}

// binaryFromTarGz 返回 tar.gz 中第一个常规文件的内容（归档内仅含单个二进制）。
func binaryFromTarGz(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("解压更新包失败: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("解压更新包失败: %w", err)
		}
		if hdr.Typeflag == tar.TypeReg {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("更新包中未找到可执行文件")
}

// binaryFromZip 返回 zip 中第一个 .exe 文件的内容。
func binaryFromZip(data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("解压更新包失败: %w", err)
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("解压更新包失败: %w", err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("读取更新包失败: %w", err)
		}
		return b, nil
	}
	return nil, fmt.Errorf("更新包中未找到可执行文件")
}

// DefaultTimeout 是一次检查/更新操作的建议超时。
const DefaultTimeout = 60 * time.Second
