package main

import (
	"os"
	"path"
	"sort"
	"strings"
)

// Reuse the same checked UI assets and explicit relationships as the App view.
// Importing this manifest never counts as a fresh Codex review of the source.
func seedProjectGraph(item project, inventory projectInventory) planDocument {
	doc := planDocument{Summary: "项目清单已建立。已有界面与源码映射已导入；继续完整分析可补齐未分析模块和跨模块关系。", Kind: item.Kind, Stack: item.Stack, Nodes: []planNode{}, Edges: []planEdge{}, Questions: []string{}}
	for _, module := range inventory.Modules {
		evidence := []planEvidence{}
		if module.Path != "." {
			evidence = append(evidence, planEvidence{File: module.Path})
		}
		doc.Nodes = append(doc.Nodes, planNode{ID: module.ID, Title: module.Title, Kind: "module", Description: "目录模块 · 等待逐文件分析", State: "unknown", Evidence: evidence, Acceptance: []string{}, ModuleID: module.ID, Origin: "scan"})
	}
	root, err := os.OpenRoot(item.Root)
	if err != nil {
		return doc
	}
	defer root.Close()
	gallery := readAppUI(root, item)
	idx := openCodeIndex(root)
	kindFor := func(kind string) string {
		switch kind {
		case "UI 页面":
			return "component"
		case "UI 交互":
			return "feature"
		case "后端接口":
			return "api"
		case "客户端存储", "数据存储", "数据模型":
			return "storage"
		case "测试":
			return "test"
		}
		return "component"
	}
	keys := []string{}
	for key := range idx.Nodes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		spec := idx.Nodes[key]
		if !codeKinds[spec.Kind] || spec.Title == "" {
			continue
		}
		evidence := []planEvidence{{File: spec.File, Match: spec.Match}}
		for _, e := range spec.Evidence {
			if len(evidence) < 10 {
				evidence = append(evidence, planEvidence{File: e.File, Match: e.Match})
			}
		}
		doc.Nodes = append(doc.Nodes, planNode{ID: "native-" + key, Title: spec.Title, Kind: kindFor(spec.Kind), Category: spec.Kind, Description: "来自项目已有的 App 源码映射", State: "existing", Evidence: evidence, Acceptance: []string{}, Origin: "manifest"})
	}
	for _, key := range keys {
		spec := idx.Nodes[key]
		for _, link := range spec.Links {
			evidence := []planEvidence{}
			for _, e := range link.Evidence {
				if len(evidence) < 6 {
					evidence = append(evidence, planEvidence{File: e.File, Match: e.Match})
				}
			}
			doc.Edges = append(doc.Edges, planEdge{From: "native-" + link.From, To: "native-" + link.To, Label: link.Label, Evidence: evidence, Origin: "manifest", Scopes: []string{"native-" + key}})
		}
		if len(spec.Links) > 0 {
			continue
		}
		for _, dep := range spec.Uses {
			doc.Edges = append(doc.Edges, planEdge{From: "native-" + key, To: "native-" + dep, Label: "配置依赖", Evidence: []planEvidence{}, Origin: "manifest"})
		}
	}
	for _, group := range gallery.Groups {
		primary := ""
		for i, screen := range group.Screens {
			id := "screen-" + checksum(group.Title+"/"+screen.Title)
			kind := "feature"
			if i == 0 {
				kind = "page"
				primary = id
			}
			evidence := []planEvidence{}
			if screen.Source != "" {
				evidence = append(evidence, planEvidence{File: screen.Source})
			}
			preview := &planPreview{Group: group.Title, Screen: screen.Title, Captures: []planCapture{}}
			for _, capture := range screen.Captures {
				if screenshotExtensions[strings.ToLower(path.Ext(capture.File))] != "" && regularDemoPath(root, capture.File, false) {
					preview.Captures = append(preview.Captures, planCapture{Label: capture.Label, URL: "/api/projects/" + item.ID + "/app-ui-image/" + checksum(capture.File)})
				}
			}
			if strings.HasPrefix(screen.Demo, "demos/") && (path.Ext(screen.Demo) == ".html" || path.Ext(screen.Demo) == ".htm") && regularDemoPath(root, screen.Demo, false) {
				preview.Demo = screen.Demo
			}
			doc.Nodes = append(doc.Nodes, planNode{ID: id, Title: screen.Title, Kind: kind, Category: "App 界面", Description: group.Title + " · 界面与功能入口", State: "existing", Evidence: evidence, Acceptance: []string{}, Origin: "manifest", Preview: preview})
			if i > 0 {
				doc.Edges = append(doc.Edges, planEdge{From: primary, To: id, Label: "界面分组", Evidence: evidence, Origin: "manifest"})
			}
			for _, key := range idx.Features[group.Title+"/"+screen.Title] {
				if spec, ok := idx.Nodes[key]; ok {
					doc.Edges = append(doc.Edges, planEdge{From: id, To: "native-" + key, Label: "功能实现", Evidence: []planEvidence{{File: spec.File, Match: spec.Match}}, Origin: "manifest"})
				}
			}
		}
	}
	return normalizeSeed(doc, inventory)
}
func normalizeSeed(doc planDocument, inventory projectInventory) planDocument {
	modules := map[string]string{}
	for _, f := range inventory.Files {
		modules[f.Path] = f.ModuleID
	}
	nodes := map[string]bool{}
	for i := range doc.Nodes {
		n := &doc.Nodes[i]
		nodes[n.ID] = true
		for _, e := range n.Evidence {
			if id := modules[e.File]; id != "" {
				n.ModuleID = id
				break
			}
		}
	}
	edges := []planEdge{}
	seen := map[string]int{}
	for _, e := range doc.Edges {
		key := e.From + "\x00" + e.To + "\x00" + e.Label
		if nodes[e.From] && nodes[e.To] && e.From != e.To {
			if index, exists := seen[key]; exists {
				edges[index].Scopes = mergePlanScopes(edges[index].Scopes, e.Scopes)
				continue
			}
			seen[key] = len(edges)
			edges = append(edges, e)
		}
	}
	doc.Edges = edges
	return doc
}
