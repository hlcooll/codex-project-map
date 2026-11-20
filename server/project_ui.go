package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

type uiReference struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
}
type uiAsset struct {
	ID         string        `json:"id"`
	File       string        `json:"file"`
	URL        string        `json:"url"`
	Label      string        `json:"label"`
	Source     string        `json:"source"`
	References []uiReference `json:"references"`
	Revision   string        `json:"revision"`
}
type projectUIView struct {
	Total       int       `json:"total"`
	WithPreview int       `json:"withPreview"`
	WithCapture int       `json:"withCapture"`
	Assets      []uiAsset `json:"assets"`
	Warnings    []string  `json:"warnings"`
}

// URLs are always server-derived. Model bindings contain only existing relative paths.
type uiBinding struct {
	NodeID   string         `json:"nodeId"`
	AssetID  string         `json:"assetId,omitempty"`
	File     string         `json:"file"`
	Label    string         `json:"label"`
	Kind     string         `json:"kind"`
	Reason   string         `json:"reason"`
	Evidence []planEvidence `json:"evidence"`
	Revision string         `json:"revision,omitempty"`
	Source   string         `json:"source,omitempty"`
}

var uiMarkdownImage = regexp.MustCompile(`!?\[[^\]]*\]\(([^)]+)\)`)

func previewKind(kind string) bool {
	return kind == "capture" || kind == "render" || kind == "reference"
}
func projectAssetID(file string) string { return "file-" + checksum(file) }

// Discover media independently of the source inventory. Media never inflates code coverage.
func discoverUIAssets(item project) ([]uiAsset, []string) {
	assets := []uiAsset{}
	warnings := []string{}
	root, err := os.OpenRoot(item.Root)
	if err != nil {
		return assets, []string{"预览素材目录无法读取"}
	}
	defer root.Close()
	candidates := map[string]bool{}
	docs := []string{}
	entries := 0
	capped := false
	var walk func(string, int, bool)
	walk = func(dir string, depth int, media bool) {
		if depth > 8 || entries >= 10000 {
			capped = true
			return
		}
		f, e := root.Open(dir)
		if e != nil {
			return
		}
		list, e := f.ReadDir(10001 - entries)
		f.Close()
		if e != nil && e != io.EOF {
			return
		}
		entries += len(list)
		sort.Slice(list, func(i, j int) bool { return list[i].Name() < list[j].Name() })
		for _, entry := range list {
			name := entry.Name()
			lower := strings.ToLower(name)
			if strings.HasPrefix(name, ".") || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			file := path.Join(dir, name)
			if entry.IsDir() {
				if lower == "node_modules" || lower == "vendor" || lower == "build" || lower == "dist" || lower == "target" || lower == "deriveddata" || strings.HasSuffix(lower, ".app") || strings.HasSuffix(lower, ".framework") || strings.HasSuffix(lower, ".xcassets") {
					continue
				}
				isMedia := media || lower == "previews" || lower == "preview" || lower == "screenshots" || lower == "app-ui" || lower == "demos" || lower == "demo" || lower == "stitch_app" || lower == "prototypes"
				walk(file, depth+1, isMedia)
				continue
			}
			if !entry.Type().IsRegular() {
				continue
			}
			if strings.HasSuffix(lower, ".md") && len(docs) < 100 {
				docs = append(docs, file)
			}
			if media && screenshotExtensions[strings.ToLower(path.Ext(file))] != "" {
				if len(candidates) < 240 {
					candidates[file] = true
				} else {
					capped = true
				}
			}
		}
	}
	walk(".", 0, false)
	references := map[string][]uiReference{}
	for _, doc := range docs {
		if !regularDemoPath(root, doc, false) {
			continue
		}
		data := readOverviewFile(root, doc, 128<<10)
		if data == nil {
			continue
		}
		heading := ""
		for i, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "#") {
				heading = strings.TrimLeft(line, "# ")
			}
			for _, match := range uiMarkdownImage.FindAllStringSubmatch(line, -1) {
				target := strings.Trim(match[1], "<>")
				target = strings.Split(target, " \"")[0]
				u, e := url.Parse(target)
				if e != nil || u.IsAbs() || u.Host != "" || strings.HasPrefix(u.Path, "/") {
					continue
				}
				file := path.Clean(path.Join(path.Dir(doc), u.Path))
				if !planPath(file) || screenshotExtensions[strings.ToLower(path.Ext(file))] == "" || !regularDemoPath(root, file, false) {
					continue
				}
				if len(candidates) < 240 || candidates[file] {
					candidates[file] = true
				} else {
					capped = true
					continue
				}
				if len(references[file]) < 5 {
					references[file] = append(references[file], uiReference{File: doc, Line: i + 1, Text: clipActivity(heading+" · "+line, 500)})
				}
			}
		}
	}
	// Explicit mappings may use image folders outside the discovery conventions.
	for _, group := range readAppUI(root, item).Groups {
		for _, screen := range group.Screens {
			for _, capture := range screen.Captures {
				if len(candidates) < 400 {
					candidates[capture.File] = true
				}
			}
		}
	}
	paths := []string{}
	for file := range candidates {
		paths = append(paths, file)
	}
	sort.Strings(paths)
	for _, file := range paths {
		if !planPath(file) || screenshotExtensions[strings.ToLower(path.Ext(file))] == "" || !regularDemoPath(root, file, false) {
			continue
		}
		info, e := root.Stat(file)
		if e != nil {
			continue
		}
		refs := references[file]
		if refs == nil {
			refs = []uiReference{}
		}
		id := projectAssetID(file)
		assets = append(assets, uiAsset{ID: id, File: file, URL: "/api/projects/" + item.ID + "/ui-asset/" + id, Label: strings.TrimSuffix(path.Base(file), path.Ext(file)), Source: "project", References: refs, Revision: fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())})
	}
	if capped {
		warnings = append(warnings, "素材扫描达到目录或数量上限，部分素材未列出。可上传目标页面截图。")
	}
	return assets, warnings
}
func (p *projectPlanner) uiCatalog(item project) ([]uiAsset, []string, error) {
	assets, warnings := discoverUIAssets(item)
	rows, err := p.db.Query(`SELECT id,label,created_at FROM project_ui_captures WHERE project_id=? ORDER BY created_at DESC`, item.ID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, label, at string
		if err = rows.Scan(&id, &label, &at); err != nil {
			return nil, nil, err
		}
		assets = append(assets, uiAsset{ID: id, File: label, URL: "/api/projects/" + item.ID + "/ui-asset/" + id, Label: label, Source: "capture", References: []uiReference{}, Revision: at})
	}
	return assets, warnings, rows.Err()
}
func (p *projectPlanner) attachUI(item project, doc *planDocument) (*projectUIView, error) {
	assets, warnings, err := p.uiCatalog(item)
	if err != nil {
		return nil, err
	}
	view := &projectUIView{Assets: assets, Warnings: warnings}
	byID := map[string]uiAsset{}
	for _, a := range assets {
		byID[a.ID] = a
	}
	bindings := map[string]uiBinding{}
	for _, b := range doc.UIPreviews {
		b.AssetID = projectAssetID(b.File)
		b.Source = "codex"
		bindings[b.NodeID+"\x00"+b.AssetID] = b
	}
	rows, err := p.db.Query(`SELECT data FROM project_ui_bindings WHERE project_id=?`, item.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		var b uiBinding
		if json.Unmarshal([]byte(raw), &b) == nil {
			bindings[b.NodeID+"\x00"+b.AssetID] = b
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	keys := []string{}
	for key := range bindings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i := range doc.Nodes {
		node := &doc.Nodes[i]
		if node.Kind != "page" && node.Preview == nil {
			continue
		}
		if node.Preview == nil {
			node.Preview = &planPreview{Group: "源码识别", Screen: node.Title, Captures: []planCapture{}}
		}
		// Retain explicit App mappings with the same identity used by the analysis graph.
		captures := []planCapture{}
		for _, c := range node.Preview.Captures {
			if c.Source != "" && c.Source != "manifest" {
				continue
			}
			if c.Source == "" {
				c.Source = "manifest"
				c.Kind = "capture"
				c.Reason = "项目已有界面映射；截图不代表当前运行状态"
			}
			if c.AssetID == "" {
				for _, a := range assets {
					if c.URL == "/api/projects/"+item.ID+"/app-ui-image/"+checksum(a.File) {
						c.AssetID = a.ID
						break
					}
				}
			}
			if b, ok := bindings[node.ID+"\x00"+c.AssetID]; ok {
				if b.Kind == "hidden" {
					continue
				}
				if _, exists := byID[b.AssetID]; exists {
					continue
				}
			}
			captures = append(captures, c)
		}
		for _, key := range keys {
			b := bindings[key]
			if b.NodeID != node.ID || !previewKind(b.Kind) {
				continue
			}
			asset, ok := byID[b.AssetID]
			if !ok {
				view.Warnings = append(view.Warnings, node.Title+" 的已关联素材不存在，请重新关联")
				continue
			}
			if b.Revision != "" && b.Revision != asset.Revision {
				view.Warnings = append(view.Warnings, node.Title+" 的素材文件已变化，请重新核对关联")
				continue
			}
			captures = append(captures, planCapture{Label: b.Label, URL: asset.URL, Kind: b.Kind, Source: b.Source, AssetID: asset.ID, Reason: b.Reason})
		}
		// Prefer complete interface captures over isolated renders and historic references.
		sort.SliceStable(captures, func(i, j int) bool {
			score := func(k string) int {
				if k == "capture" {
					return 0
				}
				if k == "render" {
					return 1
				}
				return 2
			}
			return score(captures[i].Kind) < score(captures[j].Kind)
		})
		node.Preview.Captures = captures
		view.Total++
		if len(captures) > 0 {
			view.WithPreview++
		}
		for _, c := range captures {
			if c.Kind == "capture" {
				view.WithCapture++
				break
			}
		}
	}
	return view, nil
}
func (p *projectPlanner) uiAssets(w http.ResponseWriter, r *http.Request) {
	item, err := p.projects.project(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	doc, _, _, err := p.document(item)
	if err != nil {
		http.Error(w, "项目档案不可读", 500)
		return
	}
	ui, err := p.attachUI(item, &doc)
	if err != nil {
		http.Error(w, "素材读取失败", 500)
		return
	}
	writeJSON(w, ui)
}
func (p *projectPlanner) uiAsset(w http.ResponseWriter, r *http.Request) {
	item, err := p.projects.project(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("asset")
	if strings.HasPrefix(id, "capture-") {
		var data []byte
		var mime, at string
		if p.db.QueryRow(`SELECT data,mime,created_at FROM project_ui_captures WHERE project_id=? AND id=?`, item.ID, id).Scan(&data, &mime, &at) != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		stamp, _ := time.Parse(time.RFC3339Nano, at)
		http.ServeContent(w, r, id, stamp, bytes.NewReader(data))
		return
	}
	assets, _ := discoverUIAssets(item)
	for _, a := range assets {
		if a.ID != id {
			continue
		}
		root, e := os.OpenRoot(item.Root)
		if e != nil {
			break
		}
		defer root.Close()
		if !regularDemoPath(root, a.File, false) {
			break
		}
		serveAppUIImage(w, r, root, a.File, screenshotExtensions[strings.ToLower(path.Ext(a.File))])
		return
	}
	http.NotFound(w, r)
}
func (p *projectPlanner) uiPage(item project, id string) bool {
	doc, _, _, err := p.document(item)
	if err != nil {
		return false
	}
	for _, n := range doc.Nodes {
		if n.ID == id {
			return n.Kind == "page" || n.Preview != nil
		}
	}
	return false
}
func (p *projectPlanner) writeUIBinding(item project, b uiBinding) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	_, err = p.db.Exec(`INSERT INTO project_ui_bindings VALUES(?,?,?,?) ON CONFLICT(project_id,node_id,asset_id) DO UPDATE SET data=excluded.data`, item.ID, b.NodeID, b.AssetID, string(raw))
	return err
}
func (p *projectPlanner) bindUI(w http.ResponseWriter, r *http.Request) {
	item, err := p.projects.project(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var b uiBinding
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&b) != nil || !previewKind(b.Kind) || !p.uiPage(item, b.NodeID) {
		http.Error(w, "请选择有效页面与预览类型", 400)
		return
	}
	assets, _, err := p.uiCatalog(item)
	if err != nil {
		http.Error(w, "素材读取失败", 500)
		return
	}
	for _, asset := range assets {
		if asset.ID != b.AssetID {
			continue
		}
		b.File = asset.File
		b.Revision = asset.Revision
		b.Source = "manual"
		b.Reason = "用户关联的预览素材"
		b.Evidence = nil
		b.Label = clipActivity(strings.TrimSpace(b.Label), 120)
		if b.Label == "" {
			b.Label = asset.Label
		}
		if err = p.writeUIBinding(item, b); err != nil {
			http.Error(w, "保存预览失败", 500)
			return
		}
		writeJSON(w, map[string]bool{"saved": true})
		return
	}
	http.Error(w, "素材不存在或已被移除", 404)
}
func (p *projectPlanner) unbindUI(w http.ResponseWriter, r *http.Request) {
	item, err := p.projects.project(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	nodeID, assetID := r.URL.Query().Get("nodeId"), r.URL.Query().Get("assetId")
	if !p.uiPage(item, nodeID) || len(assetID) > 100 || assetID == "" {
		http.Error(w, "页面或素材无效", 400)
		return
	}
	// A tombstone suppresses older model or manifest bindings without deleting shared media.
	if err = p.writeUIBinding(item, uiBinding{NodeID: nodeID, AssetID: assetID, Kind: "hidden", Source: "manual"}); err != nil {
		http.Error(w, "移除关联失败", 500)
		return
	}
	writeJSON(w, map[string]bool{"saved": true})
}
func (p *projectPlanner) uploadUI(w http.ResponseWriter, r *http.Request) {
	item, err := p.projects.project(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, (8<<20)+65536)
	if r.ParseMultipartForm(8<<20) != nil {
		http.Error(w, "截图必须小于8MiB", 400)
		return
	}
	defer r.MultipartForm.RemoveAll()
	nodeID := r.FormValue("nodeId")
	if !p.uiPage(item, nodeID) {
		http.Error(w, "页面不存在", 400)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "请选择截图文件", 400)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		http.Error(w, "截图读取失败或过大", 400)
		return
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") || config.Width <= 0 || config.Height <= 0 || config.Width > 12000 || config.Height > 12000 || config.Width*config.Height > 40000000 {
		http.Error(w, "请上传有效PNG或JPEG截图，最多4000万像素", 400)
		return
	}
	if _, _, err = image.Decode(bytes.NewReader(data)); err != nil {
		http.Error(w, "截图内容损坏，请重新导出PNG或JPEG", 400)
		return
	}
	id := "capture-" + checksum(string(data))
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	label := clipActivity(strings.TrimSpace(r.FormValue("label")), 120)
	if label == "" {
		label = "界面截图 · " + time.Now().Format("2006-01-02 15:04")
	}
	b := uiBinding{NodeID: nodeID, AssetID: id, File: label, Label: label, Kind: "capture", Source: "manual", Revision: stamp, Reason: "上传的完整界面截图；非实时运行画面"}
	raw, _ := json.Marshal(b)
	tx, err := p.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "保存截图失败", 500)
		return
	}
	defer tx.Rollback()
	var count int
	err = tx.QueryRow(`SELECT count(*) FROM project_ui_captures WHERE project_id=?`, item.ID).Scan(&count)
	if err != nil {
		http.Error(w, "保存截图失败", 500)
		return
	}
	if count >= 120 {
		http.Error(w, "项目已保存120张截图，请优先使用已有素材关联", 409)
		return
	}
	_, err = tx.Exec(`INSERT INTO project_ui_captures VALUES(?,?,?,?,?,?) ON CONFLICT(project_id,id) DO NOTHING`, item.ID, id, label, "image/"+format, stamp, data)
	if err == nil {
		err = tx.QueryRow(`SELECT created_at FROM project_ui_captures WHERE project_id=? AND id=?`, item.ID, id).Scan(&b.Revision)
		raw, _ = json.Marshal(b)
	}
	if err == nil {
		_, err = tx.Exec(`INSERT INTO project_ui_bindings VALUES(?,?,?,?) ON CONFLICT(project_id,node_id,asset_id) DO UPDATE SET data=excluded.data`, item.ID, nodeID, id, string(raw))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		http.Error(w, "保存截图失败", 500)
		return
	}
	writeJSON(w, map[string]string{"assetId": id})
}

// Both screens use this exact hydrated document. No separate analyzed-page inventory.
func (p *projectPlanner) appUI(w http.ResponseWriter, r *http.Request) {
	item, err := p.projects.project(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	doc, _, _, err := p.document(item)
	if err != nil {
		http.Error(w, "项目档案不可读", 500)
		return
	}
	ui, err := p.attachUI(item, &doc)
	if err != nil {
		http.Error(w, "界面预览不可读", 500)
		return
	}
	root, err := os.OpenRoot(item.Root)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	legacy := readAppUI(root, item)
	idx := openCodeIndex(root)
	type screen struct {
		NodeID   string        `json:"nodeId"`
		Title    string        `json:"title"`
		Source   string        `json:"source,omitempty"`
		Demo     string        `json:"demo,omitempty"`
		Captures []planCapture `json:"captures"`
		Code     []codeNode    `json:"code,omitempty"`
	}
	type group struct {
		Title   string   `json:"title"`
		Primary bool     `json:"primary,omitempty"`
		Screens []screen `json:"screens"`
	}
	groups := []group{}
	indexes := map[string]int{}
	original := map[string]appScreen{}
	for _, g := range legacy.Groups {
		indexes[g.Title] = len(groups)
		groups = append(groups, group{Title: g.Title, Primary: g.Primary, Screens: []screen{}})
		for _, s := range g.Screens {
			original[g.Title+"/"+s.Title] = s
		}
	}
	for _, n := range doc.Nodes {
		if n.Preview == nil {
			continue
		}
		preview := n.Preview
		gi, ok := indexes[preview.Group]
		if !ok {
			gi = len(groups)
			indexes[preview.Group] = gi
			groups = append(groups, group{Title: preview.Group, Screens: []screen{}})
		}
		s := screen{NodeID: n.ID, Title: n.Title, Demo: preview.Demo, Captures: preview.Captures}
		for _, e := range n.Evidence {
			if e.Valid && e.Line > 0 {
				s.Source = e.File
				break
			}
		}
		if old, ok := original[preview.Group+"/"+preview.Screen]; ok {
			s.Code = idx.forScreen(preview.Group, old)
			for ci := range s.Captures {
				for _, oldCapture := range old.Captures {
					if s.Captures[ci].AssetID == projectAssetID(oldCapture.File) {
						s.Captures[ci].Mode = oldCapture.Mode
					}
				}
			}
		}
		groups[gi].Screens = append(groups[gi].Screens, s)
	}
	writeJSON(w, struct {
		Title  string         `json:"title"`
		Note   string         `json:"note"`
		Groups []group        `json:"groups"`
		UI     *projectUIView `json:"ui"`
	}{item.Name, "页面与项目图谱共用分析结果；预览类型单独标注。", groups, ui})
}

// Validated model associations are stored atomically with their graph revision.
func validateUIBindings(item project, doc planDocument, bindings []uiBinding) ([]uiBinding, error) {
	if len(bindings) > 120 {
		return nil, fmt.Errorf("单批界面预览关联超过120条")
	}
	assets, _ := discoverUIAssets(item)
	byFile := map[string]uiAsset{}
	for _, a := range assets {
		byFile[a.File] = a
	}
	pages := map[string]bool{}
	for _, n := range doc.Nodes {
		if n.Kind == "page" || n.Preview != nil {
			pages[n.ID] = true
		}
	}
	root, err := os.OpenRoot(item.Root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	out := []uiBinding{}
	for _, b := range bindings {
		asset, ok := byFile[b.File]
		if !ok || !pages[b.NodeID] || !previewKind(b.Kind) {
			return nil, fmt.Errorf("界面预览引用了未识别页面、素材或类型：%s", b.File)
		}
		if len(b.Evidence) == 0 || len(b.Evidence) > 3 {
			return nil, fmt.Errorf("界面预览需要1–3处来源依据：%s", b.File)
		}
		for i, e := range b.Evidence {
			b.Evidence[i] = validatePlanEvidence(root, e)
			if !b.Evidence[i].Valid {
				return nil, fmt.Errorf("界面预览来源依据无效：%s", e.File)
			}
		}
		b.AssetID = asset.ID
		b.Revision = asset.Revision
		b.Source = "codex"
		b.Label = clipActivity(b.Label, 120)
		b.Reason = clipActivity(b.Reason, 500)
		out = append(out, b)
	}
	return out, nil
}
func mergeUIBindings(base, delta []uiBinding) []uiBinding {
	result := append([]uiBinding{}, base...)
	index := map[string]int{}
	for i, b := range result {
		index[b.NodeID+"\x00"+b.File] = i
	}
	for _, b := range delta {
		key := b.NodeID + "\x00" + b.File
		if i, ok := index[key]; ok {
			result[i] = b
		} else {
			index[key] = len(result)
			result = append(result, b)
		}
	}
	return result
}
