package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

const appUIConfig = ".codex-app-ui.json"

var screenshotDirectories = []string{"screenshots", "docs/screenshots", "docs/app-ui"}
var screenshotExtensions = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp"}

type appCapture struct {
	Label string `json:"label"`
	Mode  string `json:"mode,omitempty"`
	File  string `json:"file"`
	URL   string `json:"url,omitempty"`
}

type appScreen struct {
	Title    string       `json:"title"`
	Source   string       `json:"source,omitempty"`
	Demo     string       `json:"demo,omitempty"`
	Captures []appCapture `json:"captures,omitempty"`
	Code     []codeNode   `json:"code,omitempty"`
}

type appScreenGroup struct {
	Title   string      `json:"title"`
	Primary bool        `json:"primary,omitempty"`
	Screens []appScreen `json:"screens"`
}

type appUI struct {
	Title  string           `json:"title"`
	Note   string           `json:"note,omitempty"`
	Groups []appScreenGroup `json:"groups"`
}

func readAppUI(root *os.Root, item project) appUI {
	gallery := appUI{Title: item.Name, Groups: []appScreenGroup{}}
	if data := readOverviewFile(root, appUIConfig, 128<<10); data != nil {
		var configured appUI
		if json.Unmarshal(data, &configured) == nil && len(configured.Groups) > 0 {
			if configured.Title == "" {
				configured.Title = item.Name
			}
			return configured
		}
	}
	group := appScreenGroup{Title: "项目截图"}
	for _, dir := range screenshotDirectories {
		if !regularDemoPath(root, dir, true) {
			continue
		}
		entries, err := fs.ReadDir(root.FS(), dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			ext := path.Ext(entry.Name())
			if screenshotExtensions[strings.ToLower(ext)] != "" && entry.Type().IsRegular() && len(group.Screens) < 48 {
				title := strings.TrimSuffix(entry.Name(), ext)
				group.Screens = append(group.Screens, appScreen{Title: title, Captures: []appCapture{{Label: "截图", File: path.Join(dir, entry.Name())}}})
			}
		}
	}
	if len(group.Screens) == 0 && item.screenshot != "" {
		if relative, ok := relativePath(item.Root, item.Root, item.screenshot); ok {
			group.Screens = append(group.Screens, appScreen{Title: item.Name, Captures: []appCapture{{Label: "截图", File: relative}}})
		}
	}
	if len(group.Screens) != 0 {
		gallery.Groups = append(gallery.Groups, group)
	}
	return gallery
}

func (p *projectRegistry) appUIImage(w http.ResponseWriter, r *http.Request) {
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
	key := r.PathValue("image")
	for _, group := range readAppUI(root, item).Groups {
		for _, screen := range group.Screens {
			for _, capture := range screen.Captures {
				mime := screenshotExtensions[strings.ToLower(path.Ext(capture.File))]
				if mime != "" && checksum(capture.File) == key && regularDemoPath(root, capture.File, false) {
					serveAppUIImage(w, r, root, capture.File, mime)
					return
				}
			}
		}
	}
	http.NotFound(w, r)
}

func serveAppUIImage(w http.ResponseWriter, r *http.Request, root *os.Root, file, mime string) {
	image, err := root.Open(file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer image.Close()
	info, err := image.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 25<<20 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, path.Base(file), info.ModTime(), image)
}
