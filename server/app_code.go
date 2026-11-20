package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"
	"unicode/utf8"
)

const appCodeConfig = ".codex-app-code.json"

var codeExtensions = map[string]bool{
	".swift": true, ".metal": true, ".go": true, ".vue": true, ".ts": true, ".tsx": true,
	".js": true, ".jsx": true, ".kt": true, ".java": true, ".dart": true, ".rs": true, ".py": true, ".sql": true,
	".c": true, ".h": true, ".cpp": true, ".hpp": true, ".cc": true, ".m": true, ".mm": true, ".cs": true,
	".rb": true, ".php": true, ".scala": true, ".ex": true, ".exs": true, ".lua": true, ".svelte": true, ".astro": true,
	".graphql": true, ".gql": true, ".proto": true, ".gradle": true, ".kts": true, ".xml": true, ".plist": true,
	".pbxproj": true, ".xcscheme": true, ".xcworkspacedata": true, ".xcconfig": true, ".entitlements": true,
}

var codeKinds = map[string]bool{
	"UI 页面": true, "UI 交互": true, "UI 组件": true, "UI 渲染": true,
	"客户端服务": true, "客户端存储": true, "客户端网络": true, "数据模型": true, "测试": true,
	"后端接口": true, "后端组件": true, "中间件": true, "数据存储": true,
}

type codeLocation struct {
	File string `json:"file"`
	Line int    `json:"line"`
	ID   string `json:"id"`
}

type codeAnchor struct {
	File  string `json:"file"`
	Match string `json:"match"`
}

type codeSpec struct {
	codeAnchor
	Title    string         `json:"title"`
	Kind     string         `json:"kind"`
	Evidence []codeAnchor   `json:"evidence"`
	Uses     []string       `json:"uses"`
	Links    []codeLinkSpec `json:"links"`
}

type codeManifest struct {
	Features map[string][]string `json:"features"`
	Nodes    map[string]codeSpec `json:"nodes"`
}

type codeNode struct {
	codeLocation
	Key      string         `json:"key,omitempty"`
	Title    string         `json:"title"`
	Kind     string         `json:"kind"`
	Evidence []codeLocation `json:"evidence,omitempty"`
	Uses     []codeNode     `json:"uses,omitempty"`
	Graph    *codeGraph     `json:"graph,omitempty"`
}

type codeIndex struct {
	root  *os.Root
	files map[string][]string
	codeManifest
}

func openCodeIndex(root *os.Root) *codeIndex {
	idx := &codeIndex{root: root, files: map[string][]string{}}
	var manifest codeManifest
	if json.Unmarshal(readOverviewFile(root, appCodeConfig, 128<<10), &manifest) == nil {
		idx.codeManifest = manifest
	}
	return idx
}

func (idx *codeIndex) lines(file string) []string {
	if lines, ok := idx.files[file]; ok {
		return lines
	}
	var lines []string
	if len(idx.files) >= 160 {
		return nil
	}
	if codeExtensions[strings.ToLower(path.Ext(file))] && regularDemoPath(idx.root, file, false) {
		if data := readOverviewFile(idx.root, file, 768<<10); data != nil && utf8.Valid(data) {
			lines = strings.Split(string(data), "\n")
		}
	}
	idx.files[file] = lines
	return lines
}

func (idx *codeIndex) locate(anchor codeAnchor) (codeLocation, bool) {
	lines := idx.lines(anchor.File)
	if len(lines) == 0 {
		return codeLocation{}, false
	}
	if anchor.Match == "" {
		return makeLocation(anchor.File, 1), true
	}
	for i, line := range lines {
		if strings.Contains(line, anchor.Match) {
			return makeLocation(anchor.File, i+1), true
		}
	}
	return codeLocation{}, false
}

func makeLocation(file string, line int) codeLocation {
	return codeLocation{file, line, checksum(fmt.Sprintf("%s:%d", file, line))}
}

func (idx *codeIndex) node(spec codeSpec) (codeNode, bool) {
	location, ok := idx.locate(spec.codeAnchor)
	if !ok || !codeKinds[spec.Kind] || spec.Title == "" {
		return codeNode{}, false
	}
	node := codeNode{codeLocation: location, Title: spec.Title, Kind: spec.Kind}
	for _, anchor := range spec.Evidence {
		if anchor.Match == "" {
			return codeNode{}, false
		}
		found, ok := idx.locate(anchor)
		if !ok {
			return codeNode{}, false
		}
		node.Evidence = append(node.Evidence, found)
	}
	return node, true
}

func (idx *codeIndex) forScreen(group string, screen appScreen) []codeNode {
	var nodes []codeNode
	seen := map[string]bool{}
	covered := false
	for _, key := range idx.Features[group+"/"+screen.Title] {
		if len(nodes) >= 18 || seen[key] {
			continue
		}
		seen[key] = true
		spec, exists := idx.Nodes[key]
		// API edges need evidence in both the app and the service.
		if !exists || (spec.Kind == "后端接口" && (spec.Match == "" || len(spec.Evidence) < 2)) {
			continue
		}
		node, valid := idx.node(spec)
		if !valid {
			continue
		}
		node.Key = key
		if spec.Kind == "后端接口" && len(spec.Links) > 0 {
			node.Graph = idx.graphFor(node, spec.Links)
		} else if spec.Kind == "后端接口" {
			uses := map[string]bool{}
			for _, dep := range spec.Uses {
				if len(node.Uses) >= 14 || uses[dep] {
					continue
				}
				uses[dep] = true
				dependency := idx.Nodes[dep]
				if dependency.Kind == "后端组件" || dependency.Kind == "中间件" || dependency.Kind == "数据存储" || dependency.Kind == "数据模型" {
					if child, found := idx.node(dependency); found {
						child.Key = dep
						node.Uses = append(node.Uses, child)
					}
				}
			}
		}
		covered = covered || strings.HasPrefix(node.Kind, "UI ")
		nodes = append(nodes, node)
	}
	if !covered && screen.Source != "" {
		if node, ok := idx.node(codeSpec{codeAnchor: codeAnchor{File: screen.Source}, Title: path.Base(screen.Source), Kind: "UI 页面"}); ok {
			nodes = append([]codeNode{node}, nodes...)
		}
	}
	return nodes
}

func codeSnippet(idx *codeIndex, loc codeLocation) (int, []string) {
	lines := idx.lines(loc.File)
	start := max(1, loc.Line-2)
	end := min(len(lines), loc.Line+23)
	if start > end {
		return 0, nil
	}
	snippet := make([]string, 0, end-start+1)
	for _, line := range lines[start-1 : end] {
		if len(line) > 800 {
			runes := []rune(line)
			line = string(runes[:min(len(runes), 640)]) + "…"
		}
		snippet = append(snippet, strings.TrimSuffix(line, "\r"))
	}
	return start, snippet
}

func (p *projectRegistry) appCode(w http.ResponseWriter, r *http.Request) {
	item, err := p.project(r.PathValue("id"))
	if err != nil || item.Kind != "原生 App" {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(item.Root)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	idx := openCodeIndex(root)
	key := r.PathValue("code")
	serve := func(location codeLocation) bool {
		if location.ID != key {
			return false
		}
		start, lines := codeSnippet(idx, location)
		writeJSON(w, map[string]any{"start": start, "lines": lines})
		return true
	}
	for _, group := range readAppUI(root, item).Groups {
		for _, screen := range group.Screens {
			for _, node := range idx.forScreen(group.Title, screen) {
				for _, location := range codeNodeLocations(node) {
					if serve(location) {
						return
					}
				}
			}
		}
	}
	http.NotFound(w, r)
}
