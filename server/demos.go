package main

import (
	"context"
	"database/sql"
	"errors"
	"html"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
)

var demoDirectories = []string{"demos", "demo", "prototypes", "docs/demos", "stitch_app"}
var demoTitle = regexp.MustCompile(`(?is)<title\b[^>]*>([^<]+)</title\s*>`)
var demoExtensions = []string{".html", ".htm", ".css", ".js", ".mjs", ".png", ".jpg", ".jpeg", ".webp", ".avif", ".gif", ".svg", ".ico", ".woff2", ".woff", ".mp4", ".webm"}

type projectDemo struct {
	Title string `json:"title"`
	Path  string `json:"path"`
	URL   string `json:"url"`
	Kind  string `json:"kind"`
}

func demoFileURL(id, file string) string {
	parts := strings.Split(file, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return "/api/projects/" + id + "/demo-file/" + strings.Join(parts, "/")
}

func discoverDemos(ctx context.Context, root *os.Root, id string) []projectDemo {
	result := []projectDemo{}
	for _, folder := range demoDirectories {
		if !regularDemoPath(root, folder, true) {
			continue
		}
		visited := 0
		_ = fs.WalkDir(root.FS(), folder, func(file string, entry fs.DirEntry, err error) error {
			if ctx.Err() != nil || len(result) >= 48 || visited >= 600 {
				return fs.SkipAll
			}
			if err != nil || file == folder {
				return nil
			}
			visited++
			if skipOverviewEntry(entry) {
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				if strings.Count(strings.TrimPrefix(file, folder+"/"), "/") >= 2 {
					return fs.SkipDir
				}
				return nil
			}
			extension := strings.ToLower(path.Ext(file))
			if extension != ".html" && extension != ".htm" {
				return nil
			}
			asset, kind := file, "html"
			if folder == "stitch_app" {
				asset = path.Join(path.Dir(file), "screen.png")
				if entry.Name() != "code.html" || !regularDemoPath(root, asset, false) {
					return nil
				}
				kind = "image"
			}
			title := strings.TrimSuffix(entry.Name(), path.Ext(file))
			if title == "index" {
				title = path.Base(path.Dir(file))
			}
			if match := demoTitle.FindSubmatch(readOverviewFile(root, file, 2<<20)); match != nil {
				title = shortOverviewText(html.UnescapeString(string(match[1])), 100)
			}
			result = append(result, projectDemo{Title: title, Path: file, URL: demoFileURL(id, asset), Kind: kind})
			return nil
		})
	}
	return result
}

// Check each path component before serving files. OpenRoot also confines the file open to the project tree.
func regularDemoPath(root *os.Root, name string, directory bool) bool {
	if !fs.ValidPath(name) || name == "." {
		return false
	}
	prefix := ""
	parts := strings.Split(name, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ".") {
			return false
		}
		prefix = path.Join(prefix, part)
		info, err := root.Lstat(prefix)
		if err != nil {
			return false
		}
		if i < len(parts)-1 || directory {
			if !info.IsDir() {
				return false
			}
		} else if !info.Mode().IsRegular() || info.Size() > 25<<20 {
			return false
		}
	}
	return true
}

func (p *projectRegistry) demos(w http.ResponseWriter, r *http.Request) {
	item, err := p.project(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "无法读取项目", http.StatusInternalServerError)
		}
		return
	}
	root, err := os.OpenRoot(item.Root)
	if err != nil {
		http.Error(w, "项目目录不存在或无法读取", http.StatusUnprocessableEntity)
		return
	}
	defer root.Close()
	writeJSON(w, discoverDemos(r.Context(), root, item.ID))
}

func (p *projectRegistry) demoAsset(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	extension := strings.ToLower(path.Ext(file))
	if !slices.Contains(demoExtensions, extension) || !slices.ContainsFunc(demoDirectories, func(dir string) bool { return strings.HasPrefix(file, dir+"/") }) {
		http.NotFound(w, r)
		return
	}
	item, err := p.project(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(item.Root)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	if !regularDemoPath(root, file, false) {
		http.NotFound(w, r)
		return
	}
	asset, err := root.Open(file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer asset.Close()
	stat, err := asset.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > 25<<20 {
		http.NotFound(w, r)
		return
	}

	origin := "http://" + r.Host
	assetSource := origin + demoFileURL(item.ID, path.Dir(file)+"/")
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline' "+assetSource+
		"; style-src 'unsafe-inline' "+assetSource+"; img-src data: "+assetSource+"; font-src "+assetSource+"; media-src "+assetSource+
		"; base-uri 'none'; form-action 'none'; frame-ancestors 'self'")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, path.Base(file), stat.ModTime(), asset)
}
