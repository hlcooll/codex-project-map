package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultInventoryMaxFiles   = 10_000
	defaultInventoryMaxEntries = 100_000
	maxInventoryFileSize       = 768 * 1024
)

type projectInventory struct {
	ScannedAt   time.Time            `json:"scannedAt"`
	Fingerprint string               `json:"fingerprint"`
	Files       []inventoryFile      `json:"files"`
	Modules     []inventoryModule    `json:"modules"`
	Excluded    []inventoryExclusion `json:"excluded"`
	Warnings    []string             `json:"warnings"`
	Truncated   bool                 `json:"truncated"`
}

type inventoryFile struct {
	Path     string `json:"path"`
	ModuleID string `json:"moduleId"`
	Digest   string `json:"digest"`
	Size     int64  `json:"size"`
}

type inventoryModule struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Path  string `json:"path"`
	Files int    `json:"files"`
}

type inventoryExclusion struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

type inventoryScan struct {
	root       *os.Root
	inventory  projectInventory
	fileCounts map[string]int
	exclusions map[string]int
	maxFiles   int
	maxEntries int
	visited    int
	filesAdded int
	loaded     int
}

func scanProjectInventory(ctx context.Context, rootPath string) (projectInventory, error) {
	return scanProjectInventoryWithLimits(ctx, rootPath, defaultInventoryMaxFiles, defaultInventoryMaxEntries)
}

func scanProjectInventoryWithLimits(ctx context.Context, rootPath string, maxFiles, maxEntries int) (projectInventory, error) {
	inventory := projectInventory{
		Files:    []inventoryFile{},
		Modules:  []inventoryModule{},
		Excluded: []inventoryExclusion{},
		Warnings: []string{},
	}
	if err := ctx.Err(); err != nil {
		return inventory, err
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return inventory, fmt.Errorf("open inventory root: %w", err)
	}
	defer root.Close()

	scan := &inventoryScan{
		root:       root,
		inventory:  inventory,
		fileCounts: map[string]int{},
		exclusions: map[string]int{},
		maxFiles:   maxFiles,
		maxEntries: maxEntries,
	}
	if err := scan.walk(ctx, "."); err != nil {
		return inventory, err
	}

	sort.Slice(scan.inventory.Files, func(i, j int) bool {
		return scan.inventory.Files[i].Path < scan.inventory.Files[j].Path
	})
	scan.inventory.Modules = scan.modules()
	scan.inventory.Excluded = scan.exclusionsList()
	scan.inventory.Fingerprint = inventoryFingerprint(scan.inventory.Files)
	scan.inventory.ScannedAt = time.Now().UTC()
	return scan.inventory, nil
}

func (s *inventoryScan) walk(ctx context.Context, relDir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := s.readDirectory(relDir)
	if err != nil {
		if relDir == "." {
			return fmt.Errorf("read inventory directory %q: %w", relDir, err)
		}
		s.exclusions["不可读取的目录"]++
		s.inventory.Warnings = append(s.inventory.Warnings, "目录不可读取："+relDir)
		return nil
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.visited >= s.maxEntries {
			s.markTruncated(fmt.Sprintf("访问项达到上限 %d，扫描已截断", s.maxEntries))
			return nil
		}
		s.visited++

		relPath := entry.Name()
		if relDir != "." {
			relPath = path.Join(relDir, entry.Name())
		}
		if reason := inventoryCredentialReason(relPath); reason != "" {
			s.exclusions[reason]++
			continue
		}
		if dirReason := inventoryExcludedDirectoryReason(entry.Name()); entry.IsDir() && dirReason != "" {
			s.exclusions[dirReason]++
			continue
		}
		if inventoryHiddenPath(relPath) {
			s.exclusions["隐藏文件或目录"]++
			continue
		}
		if reason := inventoryExcludedFileReason(entry.Name()); !entry.IsDir() && reason != "" {
			s.exclusions[reason]++
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			s.exclusions["符号链接"]++
			continue
		}
		if entry.IsDir() {
			if err := s.walk(ctx, relPath); err != nil {
				return err
			}
			continue
		}
		if !entry.Type().IsRegular() {
			s.exclusions["非普通文件"]++
			continue
		}
		if !planPath(relPath) {
			s.exclusions["不支持的文件类型"]++
			continue
		}
		if s.filesAdded >= s.maxFiles {
			s.markTruncated(fmt.Sprintf("可读文件达到上限 %d，扫描已截断", s.maxFiles))
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			s.exclusions["不可读取的文件"]++
			s.inventory.Warnings = append(s.inventory.Warnings, "文件不可读取："+relPath)
			continue
		}
		if info.Size() == 0 {
			s.exclusions["空文件"]++
			continue
		}
		if info.Size() > maxInventoryFileSize {
			s.inventory.Warnings = append(s.inventory.Warnings,
				fmt.Sprintf("文件超过 768 KiB，已排除：%s", filepath.ToSlash(relPath)))
			s.exclusions["文件超过 768 KiB"]++
			continue
		}

		contents := planFile(s.root, relPath)
		if contents == nil || len(contents) == 0 {
			s.exclusions["无法读取或为空文件"]++
			continue
		}
		if len(contents) > maxInventoryFileSize {
			s.inventory.Warnings = append(s.inventory.Warnings,
				fmt.Sprintf("文件超过 768 KiB，已排除：%s", filepath.ToSlash(relPath)))
			s.exclusions["文件超过 768 KiB"]++
			continue
		}

		modulePath := inventoryModulePath(relPath)
		moduleID := inventoryModuleID(modulePath)
		s.inventory.Files = append(s.inventory.Files, inventoryFile{
			Path:     relPath,
			ModuleID: moduleID,
			Digest:   checksum(string(contents)),
			Size:     int64(len(contents)),
		})
		s.fileCounts[modulePath]++
		s.filesAdded++
	}
	return nil
}

// Reserve directory entries against one global budget before retaining them,
// including entries waiting on recursive traversal. Never read an unbounded directory.
func (s *inventoryScan) readDirectory(name string) ([]fs.DirEntry, error) {
	dir, err := s.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries := []fs.DirEntry{}
	for {
		remaining := s.maxEntries - s.loaded
		chunk, readErr := dir.ReadDir(min(512, remaining+1))
		if len(chunk) > remaining {
			chunk = chunk[:remaining]
			s.markTruncated(fmt.Sprintf("目录项达到上限 %d，扫描已截断", s.maxEntries))
		}
		s.loaded += len(chunk)
		entries = append(entries, chunk...)
		if readErr != nil && readErr != io.EOF {
			return nil, readErr
		}
		if readErr == io.EOF || s.loaded >= s.maxEntries {
			if readErr != io.EOF {
				s.markTruncated(fmt.Sprintf("目录项达到上限 %d，扫描已截断", s.maxEntries))
			}
			break
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

func (s *inventoryScan) markTruncated(warning string) {
	if s.inventory.Truncated {
		return
	}
	s.inventory.Truncated = true
	s.inventory.Warnings = append(s.inventory.Warnings, warning)
}

func (s *inventoryScan) modules() []inventoryModule {
	modules := make([]inventoryModule, 0, len(s.fileCounts))
	for modulePath, count := range s.fileCounts {
		title := modulePath
		if modulePath == "." {
			title = "root"
		}
		modules = append(modules, inventoryModule{
			ID:    inventoryModuleID(modulePath),
			Title: title,
			Path:  modulePath,
			Files: count,
		})
	}
	sort.Slice(modules, func(i, j int) bool {
		return modules[i].Path < modules[j].Path
	})
	return modules
}

func (s *inventoryScan) exclusionsList() []inventoryExclusion {
	exclusions := make([]inventoryExclusion, 0, len(s.exclusions))
	for reason, count := range s.exclusions {
		exclusions = append(exclusions, inventoryExclusion{Reason: reason, Count: count})
	}
	sort.Slice(exclusions, func(i, j int) bool {
		if exclusions[i].Reason == exclusions[j].Reason {
			return exclusions[i].Count < exclusions[j].Count
		}
		return exclusions[i].Reason < exclusions[j].Reason
	})
	return exclusions
}

func inventoryFingerprint(files []inventoryFile) string {
	files = append([]inventoryFile(nil), files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	var builder strings.Builder
	for _, file := range files {
		builder.WriteString(file.Path)
		builder.WriteByte(0)
		builder.WriteString(file.Digest)
		builder.WriteByte('\n')
	}
	return checksum(builder.String())
}

func inventoryModulePath(file string) string {
	parts := strings.Split(file, "/")
	if len(parts) == 1 {
		return "."
	}
	if inventoryContainerDirectory(parts[0]) && len(parts) > 2 {
		return path.Join(parts[0], parts[1])
	}
	return parts[0]
}

func inventoryModuleID(modulePath string) string {
	return checksum("module:" + modulePath)
}

func inventoryContainerDirectory(name string) bool {
	switch strings.ToLower(name) {
	case "src", "backend", "apps", "packages", "services", "libs", "cmd", "internal":
		return true
	default:
		return false
	}
}

func inventoryHiddenPath(relPath string) bool {
	for _, part := range strings.Split(relPath, "/") {
		if strings.HasPrefix(part, ".") && part != "." && part != ".." {
			return true
		}
	}
	return false
}

func inventoryCredentialReason(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	base := strings.ToLower(path.Base(filepath.ToSlash(name)))
	switch base {
	case ".env", ".env.local", ".env.development", ".env.production", ".env.test",
		".envrc", ".netrc", ".npmrc", ".pypirc", ".git-credentials",
		".htpasswd", "credentials", "credentials.json", "secrets", "secrets.json",
		"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519",
		"service-account.json", "service_account.json":
		return "凭据文件"
	}
	if strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") ||
		strings.HasSuffix(base, ".p12") || strings.HasSuffix(base, ".pfx") ||
		strings.HasSuffix(base, ".jks") || strings.HasSuffix(base, ".keystore") {
		return "凭据文件"
	}
	if strings.Contains(lower, "/.ssh/") || strings.HasPrefix(lower, ".ssh/") || base == ".ssh" {
		return "凭据文件"
	}
	return ""
}

func inventoryExcludedDirectoryReason(name string) string {
	lower := strings.ToLower(name)
	for _, suffix := range []string{".app", ".framework", ".xcframework", ".xcarchive", ".dSYM"} {
		if strings.HasSuffix(lower, strings.ToLower(suffix)) {
			return "生成、缓存或依赖目录"
		}
	}
	switch lower {
	case "node_modules", "vendor", "dist", "build", ".build", "target", "output",
		"test-results", "coverage", "caches", "cache", "tmp", "temp",
		".next", ".nuxt", ".turbo", ".parcel-cache", ".vite", ".pytest_cache",
		".mypy_cache", ".ruff_cache", ".cache", "__pycache__", "deriveddata":
		return "生成、缓存或依赖目录"
	default:
		return ""
	}
}

func inventoryExcludedFileReason(name string) string {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "package-lock.") || strings.HasPrefix(lower, "npm-shrinkwrap.") ||
		strings.HasPrefix(lower, "pnpm-lock.") || strings.HasPrefix(lower, "yarn.lock") ||
		strings.HasPrefix(lower, "bun.lock") || strings.HasPrefix(lower, "cargo.lock") ||
		strings.HasPrefix(lower, "go.sum") || strings.HasPrefix(lower, "composer.lock") ||
		strings.HasPrefix(lower, "poetry.lock") || strings.HasPrefix(lower, "pipfile.lock") ||
		strings.HasPrefix(lower, "pubspec.lock") || strings.HasPrefix(lower, "gemfile.lock") ||
		strings.HasPrefix(lower, "packages.lock.json") || strings.HasPrefix(lower, "deno.lock") ||
		strings.HasPrefix(lower, "flake.lock") {
		return "锁文件"
	}
	ext := strings.ToLower(filepath.Ext(lower))
	dataFile := ext == ".json" || ext == ".yaml" || ext == ".yml" || ext == ".xml" || ext == ".toml" || ext == ".ini" || ext == ".txt" || ext == ".env"
	if dataFile && (strings.Contains(lower, "secret") || strings.Contains(lower, "credential") ||
		strings.Contains(lower, "private-key") || strings.Contains(lower, "private_key")) {
		return "凭据文件"
	}
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".bmp", ".tif", ".tiff",
		".svg", ".pdf", ".zip", ".gz", ".tgz", ".bz2", ".xz", ".7z", ".rar",
		".mp3", ".wav", ".ogg", ".flac", ".mp4", ".mov", ".avi", ".mkv", ".webm",
		".ttf", ".otf", ".woff", ".woff2", ".eot", ".wasm", ".bin", ".exe", ".dll",
		".dylib", ".so", ".a", ".o", ".obj", ".class", ".jar", ".db", ".sqlite",
		".sqlite3", ".DS_Store":
		return "二进制或资源文件"
	default:
		return ""
	}
}
