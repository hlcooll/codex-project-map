package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type module struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Group       string   `json:"group"`
	Stage       string   `json:"stage"`
	Description string   `json:"description"`
	Paths       []string `json:"paths"`
}

func readonlyDB(ctx context.Context, path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro&_query_only=1&_busy_timeout=500"}
	db, err := sql.Open("sqlite3", uri.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func relativePath(root, cwd, candidate string) (string, bool) {
	if candidate == "" || strings.ContainsAny(candidate, "\x00\r\n") {
		return "", false
	}
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(cwd, candidate)
	}
	actual := filepath.Clean(candidate)
	if resolved, err := filepath.EvalSymlinks(actual); err == nil {
		actual = resolved
	}
	relative, err := filepath.Rel(root, actual)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

func checksum(value string) string {
	hash := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", hash[:8])
}

func addPath(paths []string, target string) []string {
	for _, path := range paths {
		if target == path {
			return paths
		}
	}
	if len(paths) < 6 {
		paths = append(paths, target)
	}
	return paths
}

func loadModules(root string) ([]module, error) {
	file := filepath.Join(root, ".codex-project-map.json")
	info, err := os.Lstat(file)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
		return nil, fmt.Errorf("invalid module map file")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var config struct{ Modules []module }
	if err := json.Unmarshal(data, &config); err != nil || len(config.Modules) > 32 {
		return nil, fmt.Errorf("invalid module map")
	}
	ids := map[string]bool{"unassigned": true}
	for i := range config.Modules {
		rule := &config.Modules[i]
		if !taskID.MatchString(rule.ID) || ids[rule.ID] || rule.Name == "" || len(rule.Paths) == 0 || (rule.Group != "frontend" && rule.Group != "backend" && rule.Group != "other") {
			return nil, fmt.Errorf("invalid module rule")
		}
		ids[rule.ID] = true
		for j, path := range rule.Paths {
			relative, ok := relativePath(root, root, path)
			if !ok || filepath.IsAbs(path) || relative == "." {
				return nil, fmt.Errorf("invalid module path")
			}
			rule.Paths[j] = relative
		}
		rule.Stage = "idle"
	}
	return config.Modules, nil
}

func unknownModule() module {
	return module{ID: "unassigned", Name: "未归属", Group: "other", Stage: "idle", Description: "没有命中已知功能目录", Paths: []string{}}
}

func findModule(rules []module, path string) module {
	best, longest := module{}, 0
	for _, rule := range rules {
		for _, prefix := range rule.Paths {
			if len(prefix) > longest && (path == prefix || strings.HasPrefix(path, prefix+"/")) {
				best, longest = rule, len(prefix)
			}
		}
	}
	if longest > 0 {
		return best
	}
	segments := strings.Split(path, "/")
	ext := strings.ToLower(filepath.Ext(path))
	for i, segment := range segments {
		part := strings.ToLower(segment)
		group, name := "", ""
		switch part {
		case "features", "pages", "views", "screens", "components":
			if i+1 >= len(segments) || (i == 0 && ext == ".go") {
				continue
			}
			group, name = "frontend", "前端组件"
			if (part == "screens" || part == "pages" || part == "views") && i+1 == len(segments)-1 && ext != "" {
				name = strings.TrimSuffix(segments[i+1], ext)
				return inferredModule(group, name, path)
			}
		case "internal", "cmd", "pkg":
			if i+1 >= len(segments) {
				continue
			}
			group, name = "backend", "Go 服务"
		default:
			continue
		}
		end := i + 1
		if i+1 < len(segments)-1 || ext == "" {
			end++
			name = segments[i+1]
		}
		return inferredModule(group, name, strings.Join(segments[:end], "/"))
	}
	if ext == ".go" || filepath.Base(path) == "go.mod" || filepath.Base(path) == "go.sum" {
		return inferredModule("backend", "Go 服务", filepath.ToSlash(filepath.Dir(path)))
	}
	if isInterfaceFile(path) {
		return inferredModule("frontend", "界面与应用入口", filepath.ToSlash(filepath.Dir(path)))
	}
	return unknownModule()
}

func inferredModule(group, name, prefix string) module {
	return module{ID: group + ":" + checksum(prefix), Name: name, Group: group, Stage: "idle", Description: "由目录推断 · " + prefix, Paths: []string{}}
}

func isInterfaceFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".vue", ".tsx", ".jsx", ".svelte", ".swift", ".dart", ".kt", ".html":
		return true
	case ".css", ".ts":
		return strings.Contains("/"+path, "/src/")
	default:
		return false
	}
}

func discoverModules(root string) []module {
	modules := []module{}
	positions := map[string]int{}
	visited := 0
	filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		visited++
		if visited > 1600 || len(modules) >= 28 {
			return fs.SkipAll
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			switch entry.Name() {
			case "node_modules", "vendor", "dist", "build", "bin", "out", "Pods", "target", "testdata":
				return fs.SkipDir
			}
			if strings.HasPrefix(entry.Name(), ".") || strings.Count(rel, "/") >= 8 {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || (!isInterfaceFile(rel) && !strings.HasSuffix(rel, ".go")) {
			return nil
		}
		inferred := findModule(nil, rel)
		if inferred.Group == "other" {
			return nil
		}
		if index, exists := positions[inferred.ID]; exists {
			modules[index].Paths = addPath(modules[index].Paths, rel)
		} else {
			positions[inferred.ID] = len(modules)
			inferred.Paths = []string{rel}
			modules = append(modules, inferred)
		}
		return nil
	})
	return modules
}
