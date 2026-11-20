package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

type project struct {
	ID          string   `json:"id"`
	Root        string   `json:"root"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Stack       []string `json:"stack"`
	PreviewKind string   `json:"previewKind"`
	URL         string   `json:"-"`
	build       string
	screenshot  string
}

type projectRegistry struct {
	db        *sql.DB
	defaultID string
	mu        sync.Mutex
	monitors  map[string]*cache
	previews  map[string]string
}

func newRegistry(db *sql.DB, root string) (*projectRegistry, error) {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS projects(id TEXT PRIMARY KEY,root TEXT UNIQUE NOT NULL,preview_url TEXT NOT NULL)`); err != nil {
		return nil, err
	}
	id := checksum(root)
	if _, err := db.Exec(`INSERT OR IGNORE INTO projects VALUES (?,?,?)`, id, root, ""); err != nil {
		return nil, err
	}
	return &projectRegistry{db: db, defaultID: id, monitors: map[string]*cache{}, previews: map[string]string{}}, nil
}

func (p *projectRegistry) project(id string) (project, error) {
	if id == "" {
		id = p.defaultID
	}
	var root, preview string
	if err := p.db.QueryRow(`SELECT root,preview_url FROM projects WHERE id=?`, id).Scan(&root, &preview); err != nil {
		return project{}, err
	}
	return inspectProject(id, root, preview), nil
}

func (p *projectRegistry) monitor(id string) *cache {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.monitors[id] == nil {
		p.monitors[id] = &cache{}
	}
	return p.monitors[id]
}

func (p *projectRegistry) list(w http.ResponseWriter, r *http.Request) {
	rows, err := p.db.QueryContext(r.Context(), `SELECT id,root,preview_url FROM projects ORDER BY rowid`)
	if err != nil {
		http.Error(w, "无法读取项目列表", 500)
		return
	}
	defer rows.Close()
	projects := []project{}
	for rows.Next() {
		var id, root, preview string
		if err := rows.Scan(&id, &root, &preview); err != nil {
			http.Error(w, "无法读取项目列表", 500)
			return
		}
		projects = append(projects, inspectProject(id, root, preview))
	}
	if rows.Err() != nil {
		http.Error(w, "无法读取项目列表", 500)
		return
	}
	writeJSON(w, projects)
}

func (p *projectRegistry) add(w http.ResponseWriter, r *http.Request) {
	var input struct{ Path, PreviewUrl string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input); err != nil {
		http.Error(w, "项目地址无效", 400)
		return
	}
	input.Path = strings.TrimSpace(input.Path)
	if strings.HasPrefix(input.Path, "~/") {
		home, _ := os.UserHomeDir()
		input.Path = filepath.Join(home, input.Path[2:])
	}
	if !filepath.IsAbs(input.Path) {
		http.Error(w, "请输入本地项目目录的绝对路径", 400)
		return
	}
	root, err := filepath.EvalSymlinks(input.Path)
	if err != nil || !isDirectory(root) || filepath.Dir(root) == root {
		http.Error(w, "该项目目录不存在或无法读取", 400)
		return
	}
	preview := strings.TrimSpace(input.PreviewUrl)
	if preview != "" {
		u, err := url.Parse(preview)
		_, port, splitErr := net.SplitHostPort(r.Host)
		if err != nil || u.Scheme != "http" || u.User != nil || (u.Hostname() != "localhost" && !net.ParseIP(u.Hostname()).IsLoopback()) || u.Port() == "" || (splitErr == nil && u.Port() == port) {
			http.Error(w, "预览地址需要是带端口的本机 HTTP 地址", 400)
			return
		}
	}
	id := checksum(root)
	if _, err := p.db.ExecContext(r.Context(), `INSERT INTO projects VALUES (?,?,?) ON CONFLICT(id) DO UPDATE SET preview_url=CASE WHEN excluded.preview_url='' THEN projects.preview_url ELSE excluded.preview_url END`, id, root, preview); err != nil {
		http.Error(w, "无法登记项目", 500)
		return
	}
	value, err := p.project(id)
	if err != nil {
		http.Error(w, "无法读取已登记项目", 500)
		return
	}
	writeJSON(w, value)
}

func (p *projectRegistry) preview(w http.ResponseWriter, r *http.Request) {
	selected, err := p.project(r.PathValue("id"))
	if err != nil || selected.PreviewKind == "none" {
		http.Error(w, "项目暂无可预览的界面", 404)
		return
	}
	if selected.PreviewKind == "image" {
		http.ServeFile(w, r, selected.screenshot)
		return
	}
	if selected.URL != "" {
		writeJSON(w, map[string]string{"url": selected.URL})
		return
	}
	address, err := p.serveBuild(selected)
	if err != nil {
		http.Error(w, "无法打开本机构建预览", 500)
		return
	}
	writeJSON(w, map[string]string{"url": address})
}

func (p *projectRegistry) serveBuild(project project) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if live := p.previews[project.ID]; live != "" {
		return live, nil
	}
	if len(p.previews) >= 12 {
		return "", fmt.Errorf("preview limit reached")
	}
	root, err := os.OpenRoot(project.build)
	if err != nil {
		return "", err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		root.Close()
		return "", err
	}
	address := listener.Addr().String()
	files := http.FileServerFS(root.FS())
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != address || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			http.Error(w, "Invalid preview request", 403)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		asset := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if asset == "" {
			asset = "index.html"
		}
		info, err := fs.Stat(root.FS(), asset)
		if err != nil || info.IsDir() {
			if path.Ext(asset) != "" {
				http.NotFound(w, r)
				return
			}
			r.URL.Path = "/"
		}
		files.ServeHTTP(w, r)
	})}
	go func() {
		defer root.Close()
		_ = server.Serve(listener)
	}()
	p.previews[project.ID] = "http://" + address + "/"
	return p.previews[project.ID], nil
}

func inspectProject(id, root, preview string) project {
	item := project{ID: id, Root: root, Name: filepath.Base(root), Kind: "代码项目", Stack: []string{}, PreviewKind: "none", URL: preview}
	stack := func(label string) {
		for _, previous := range item.Stack {
			if previous == label {
				return
			}
		}
		item.Stack = append(item.Stack, label)
	}
	for _, folder := range []string{"", "web", "frontend", "client", "app", "server", "backend"} {
		base := filepath.Join(root, folder)
		if _, inside := relativePath(root, root, base); !inside {
			continue
		}
		if regularFile(filepath.Join(base, "go.mod")) {
			stack("Go")
		}
		if regularFile(filepath.Join(base, "pubspec.yaml")) {
			item.Kind = "原生 App"
			stack("Flutter")
		}
		if regularFile(filepath.Join(base, "settings.gradle")) || regularFile(filepath.Join(base, "settings.gradle.kts")) {
			item.Kind = "原生 App"
			stack("Android")
		}
		if item.build == "" {
			for _, directory := range []string{"dist", "build", "out"} {
				candidate := filepath.Join(base, directory)
				if regularFile(filepath.Join(candidate, "index.html")) {
					if _, inside := relativePath(root, root, candidate); inside {
						item.build = candidate
						break
					}
				}
			}
		}
		file := filepath.Join(base, "package.json")
		info, err := os.Lstat(file)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 128*1024 {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var packages struct{ Dependencies, DevDependencies map[string]string }
		if json.Unmarshal(data, &packages) != nil {
			continue
		}
		for _, entry := range []struct{ dep, label string }{{"vue", "Vue"}, {"react", "React"}, {"svelte", "Svelte"}, {"vite", "Vite"}, {"next", "Next.js"}, {"react-native", "React Native"}} {
			if packages.Dependencies[entry.dep] != "" || packages.DevDependencies[entry.dep] != "" {
				stack(entry.label)
				if entry.dep == "react-native" {
					item.Kind = "原生 App"
				} else if item.Kind != "原生 App" {
					item.Kind = "Web 应用"
				}
			}
		}
	}
	for _, folder := range []string{"", "ios"} {
		base := filepath.Join(root, folder)
		if _, inside := relativePath(root, root, base); !inside {
			continue
		}
		entries, _ := os.ReadDir(base)
		for _, entry := range entries {
			if entry.IsDir() && filepath.Ext(entry.Name()) == ".xcodeproj" {
				item.Kind = "原生 App"
				stack("Xcode")
				break
			}
		}
	}
	if item.build != "" || preview != "" {
		item.PreviewKind = "web"
		if item.Kind == "代码项目" {
			item.Kind = "Web 应用"
		}
		return item
	}
	for _, candidate := range []string{"screenshot.png", "preview.png", "docs/screenshot.png", "docs/preview.png", "docs/screenshot-dark.png", "docs/screenshot-light.png"} {
		file := filepath.Join(root, candidate)
		if info, err := os.Lstat(file); err == nil && info.Mode().IsRegular() && info.Size() < 20_000_000 {
			if _, inside := relativePath(root, root, file); inside {
				item.PreviewKind, item.screenshot = "image", file
				break
			}
		}
	}
	if item.Kind == "代码项目" && len(item.Stack) == 1 && item.Stack[0] == "Go" {
		item.Kind = "Go 服务"
	}
	return item
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}
