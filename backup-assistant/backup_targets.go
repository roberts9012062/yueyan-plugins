// marketplace-repo/backup-assistant/backup_targets.go
// 备份目标采集：数据库 pg_dump、前端/后端源代码与构建产物的流式 ZIP 打包。
//
// 说明：
//   - 数据库连接信息优先取宿主环境变量（宿主启动脚本已 source .env，子进程继承），
//     缺失时兜底解析工作目录 .env 文件；
//   - pg_dump 路径：设置项 pg_dump_path 优先 → PATH 探测 → Windows 常见安装目录探测；
//   - 目录打包为流式写入（zip 条目边读边拷，不整驻内存——备份体积可能达数百 MB）。
package main

import (
	"archive/zip"
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// 备份产物内的条目前缀（ZIP 内目录结构约定）。
const (
	entryDatabase     = "db.dump"        // pg_dump -Fc 自定义压缩格式
	entryMediaPrefix  = "media/"         // 站点媒体库
	entryFrontendSrc  = "frontend-src/"  // 前端源代码
	entryFrontendDist = "frontend-dist/" // 前端构建产物（.next）
	entryBackendSrc   = "backend-src/"   // 后端 Go 源代码
	entryBackendDist  = "backend-dist/"  // 后端二进制（server.exe）
)

// 后端源代码白名单（相对宿主工作目录；目录递归 + 顶层文件）。
var backendSourceDirs = []string{"cmd", "internal", "pkg", "db", "scripts"}
var backendSourceFiles = []string{"go.mod", "go.sum"}

// 前端源代码排除目录（node_modules 与构建产物不进源码部分）。
var frontendSrcExcludes = []string{"node_modules", ".next", ".git", ".wrangler"}

// 前端构建产物排除目录（.next 内 cache 可再生成）。
var frontendDistExcludes = []string{"cache"}

// pgConfig PostgreSQL 连接配置（从环境变量或 .env 解析）。
type pgConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	DB       string
}

// countingWriter 计数字节包装（统计各部分 zip 条目大小）。
type countingWriter struct {
	w io.Writer
	n int64
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.n += int64(n)
	return n, err
}

// resolvePgConfig 解析数据库连接配置（优先级：插件设置 db_user/db_password >
// 宿主环境变量 > 工作目录 .env）。任一必填键（host/user/db）为空返回错误（端口缺省 5432）。
func resolvePgConfig(cfg map[string]string) (pgConfig, error) {
	pg := pgConfig{
		Host:     os.Getenv("POSTGRES_HOST"),
		Port:     os.Getenv("POSTGRES_PORT"),
		User:     os.Getenv("POSTGRES_USER"),
		Password: os.Getenv("POSTGRES_PASSWORD"),
		DB:       os.Getenv("POSTGRES_DB"),
	}
	// 兜底：宿主可能未经脚本直接启动（未 source .env），解析工作目录 .env 补齐
	envMap := parseDotEnv(".env")
	if pg.Host == "" {
		pg.Host = envMap["POSTGRES_HOST"]
	}
	if pg.Port == "" {
		pg.Port = envMap["POSTGRES_PORT"]
	}
	if pg.User == "" {
		pg.User = envMap["POSTGRES_USER"]
	}
	if pg.Password == "" {
		pg.Password = envMap["POSTGRES_PASSWORD"]
	}
	if pg.DB == "" {
		pg.DB = envMap["POSTGRES_DB"]
	}
	// 备份专用账号（插件设置优先——独立于宿主连接凭据的最小权限只读账号）
	if user := strings.TrimSpace(cfg["db_user"]); user != "" {
		pg.User = user
		if password := cfg["db_password"]; password != "" {
			pg.Password = password
		}
	}
	if pg.Host == "" || pg.User == "" || pg.DB == "" {
		return pg, errors.New("无法解析 PostgreSQL 连接配置（环境变量与 .env 均缺失 POSTGRES_HOST/USER/DB）")
	}
	if pg.Port == "" {
		pg.Port = "5432"
	}
	return pg, nil
}

// parseDotEnv 解析 .env 文件（KEY=VALUE 行；忽略注释与空行；文件不存在返回空表）。
func parseDotEnv(path string) map[string]string {
	result := make(map[string]string)
	f, err := os.Open(path)
	if err != nil {
		return result
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		result[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return result
}

// locatePgDump 定位 pg_dump 可执行文件：配置路径 → PATH → Windows 常见安装目录。
func locatePgDump(configured string) (string, error) {
	if path := strings.TrimSpace(configured); path != "" {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		return "", fmt.Errorf("配置的 pg_dump 路径不存在：%s", path)
	}
	if path, err := exec.LookPath("pg_dump"); err == nil {
		return path, nil
	}
	if runtime.GOOS == "windows" {
		// C:\Program Files\PostgreSQL\<版本>\bin\pg_dump.exe——取字典序最大（最新版本）
		matches, _ := filepath.Glob(`C:\Program Files\PostgreSQL\*\bin\pg_dump.exe`)
		if len(matches) > 0 {
			return matches[len(matches)-1], nil
		}
	}
	return "", errors.New("未找到 pg_dump（可在插件设置中配置路径，或安装 PostgreSQL 客户端工具）")
}

// dumpDatabase 执行 pg_dump 导出数据库（-Fc 自定义压缩格式，pg_restore 可恢复）。
// 输出流式写入 w；密码经 PGPASSWORD 环境变量传递（不落命令行）。
func dumpDatabase(w io.Writer, cfg map[string]string) (int64, error) {
	pg, err := resolvePgConfig(cfg)
	if err != nil {
		return 0, err
	}
	bin, err := locatePgDump(cfg["pg_dump_path"])
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(bin,
		"--host", pg.Host, "--port", pg.Port,
		"--username", pg.User, "--dbname", pg.DB,
		"--format", "custom", "--no-password",
	)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+pg.Password)
	var stderr strings.Builder
	counter := &countingWriter{w: w}
	cmd.Stdout = counter
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = err.Error()
		}
		return counter.n, fmt.Errorf("pg_dump 执行失败：%s", reason)
	}
	return counter.n, nil
}

// newDirSkip 构造路径段排除函数：相对路径任一段命中排除集合则跳过。
func newDirSkip(excludes ...string) func(rel string) bool {
	skipSet := make(map[string]bool, len(excludes))
	for _, name := range excludes {
		skipSet[name] = true
	}
	return func(rel string) bool {
		if rel == "." {
			return false
		}
		for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
			if skipSet[seg] {
				return true
			}
		}
		return false
	}
}

// zipTree 把目录树流式写入 ZIP（条目名 = prefix + 目录内相对路径）。
// skip 为 nil 时不排除；返回写入的总字节数。
func zipTree(zw *zip.Writer, root string, prefix string, skip func(rel string) bool) (int64, error) {
	var total int64
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil // 根目录自身无条目
		}
		if skip != nil && skip(rel) {
			if info.IsDir() {
				return filepath.SkipDir // 目录级剪枝（整棵子树不遍历）
			}
			return nil
		}
		if info.IsDir() {
			return nil // 目录由文件条目隐含
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		entry, err := zw.Create(prefix + filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		n, err := io.Copy(entry, f)
		total += n
		return err
	})
	return total, err
}

// zipSingleFile 把单个文件写入 ZIP（条目名 = prefix + 文件名；用于后端二进制）。
func zipSingleFile(zw *zip.Writer, path string, prefix string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	entry, err := zw.Create(prefix + filepath.Base(path))
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(entry, f)
	return n, err
}

// zipBackendSource 打包后端源代码（白名单目录 + 顶层模块文件，逐项写入）。
func zipBackendSource(zw *zip.Writer) (int64, error) {
	var total int64
	for _, dir := range backendSourceDirs {
		if _, err := os.Stat(dir); err != nil {
			continue // 目录不存在（精简部署）跳过
		}
		n, err := zipTree(zw, dir, entryBackendSrc+dir+"/", nil)
		total += n
		if err != nil {
			return total, err
		}
	}
	for _, file := range backendSourceFiles {
		if _, err := os.Stat(file); err != nil {
			continue
		}
		n, err := zipSingleFile(zw, file, entryBackendSrc)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// collectFrontend 打包前端源代码与构建产物（源码排除 node_modules/.next，产物排除 cache）。
// 两个部分独立计大小；任一部分缺失记为 skipped（原因写入返回值）。
func collectFrontend(zw *zip.Writer, parts backupParts) backupParts {
	if _, err := os.Stat("frontend"); err != nil {
		parts.Frontend = partStatus{State: stateSkipped, Reason: "frontend 目录不存在"}
		return parts
	}
	srcSize, srcErr := zipTree(zw, "frontend", entryFrontendSrc, newDirSkip(frontendSrcExcludes...))
	distSize, distErr := int64(0), error(nil)
	if _, err := os.Stat(filepath.Join("frontend", ".next")); err == nil {
		distSize, distErr = zipTree(zw, filepath.Join("frontend", ".next"), entryFrontendDist, newDirSkip(frontendDistExcludes...))
	} else {
		distErr = errNotExistDist
	}
	parts.Frontend = summarizePart(srcErr, distErr, srcSize+distSize,
		"前端源代码打包失败", "前端构建产物（.next）不存在（未构建）")
	return parts
}

// errNotExistDist 构建产物缺失哨兵错误（与真实 IO 错误区分——产物缺失仅记 skipped）。
var errNotExistDist = errors.New("frontend/.next 不存在")

// collectBackend 打包后端源代码与二进制产物。
func collectBackend(zw *zip.Writer, parts backupParts) backupParts {
	srcSize, srcErr := zipBackendSource(zw)
	binSize, binErr := int64(0), error(nil)
	if _, err := os.Stat("server.exe"); err == nil {
		binSize, binErr = zipSingleFile(zw, "server.exe", entryBackendDist+"/")
	} else {
		binErr = errNotExistBinary
	}
	parts.Backend = summarizePart(srcErr, binErr, srcSize+binSize,
		"后端源代码打包失败", "后端二进制（server.exe）不存在（开发模式 go run）")
	return parts
}

// errNotExistBinary 后端二进制缺失哨兵错误。
var errNotExistBinary = errors.New("server.exe 不存在")

// summarizePart 汇总两部分采集结果：主部分失败记 failed；次部分缺失不阻断（仍记 ok）。
// mainErr 非空 → failed；仅 auxErr 非空 → ok（部分内容缺失不视为失败）。
func summarizePart(mainErr error, auxErr error, size int64, mainReason string, auxReason string) partStatus {
	if mainErr != nil {
		return partStatus{State: stateFailed, Size: size, Reason: fmt.Sprintf("%s：%v", mainReason, mainErr)}
	}
	if auxErr != nil {
		// 主内容成功、辅助内容缺失：备份仍可用，原因附注便于排查
		return partStatus{State: stateOK, Size: size, Reason: auxReason}
	}
	return partStatus{State: stateOK, Size: size}
}
