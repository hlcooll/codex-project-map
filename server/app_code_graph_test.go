package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodeGraphReachabilityAndSourceAllowlist(t *testing.T) {
	dir := t.TempDir()
	source := "package api\nfunc route() {}\nfunc auth() {}\nfunc handler() {}\nfunc component() {}\nfunc orphan() {}\n// route_auth\n// auth_handler\n// handler_component\n// bad_back_edge\n// unreachable_evidence\n"
	if err := os.WriteFile(filepath.Join(dir, "api.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	idx := openCodeIndex(root)
	idx.Nodes = map[string]codeSpec{}
	for _, key := range []string{"route", "auth", "handler", "component", "orphan"} {
		idx.Nodes[key] = codeSpec{codeAnchor: codeAnchor{"api.go", "func " + key + "()"}, Title: key, Kind: "后端组件"}
	}
	idx.Nodes["unsafe"] = codeSpec{codeAnchor: codeAnchor{"../escape.go", "func escaped"}, Title: "unsafe", Kind: "后端组件"}
	link := func(from, to, match string) codeLinkSpec {
		return codeLinkSpec{From: from, To: to, Label: "调用", Evidence: []codeAnchor{{"api.go", match}}}
	}
	base, _ := idx.node(idx.Nodes["route"])
	base.Key = "route"
	// Input ordering is intentionally not topological.
	graph := idx.graphFor(base, []codeLinkSpec{
		link("handler", "component", "handler_component"), link("route", "auth", "route_auth"), link("auth", "handler", "auth_handler"),
		link("handler", "auth", "bad_back_edge"), link("component", "route", "bad_back_edge"), link("component", "component", "bad_back_edge"),
		link("route", "component", "missing_evidence"), link("route", "orphan", ""), link("route", "unsafe", "route_auth"),
		link("orphan", "component", "unreachable_evidence"), link("auth", "handler", "auth_handler"),
	})
	if len(graph.Nodes) != 4 || len(graph.Edges) != 3 {
		t.Fatalf("expected 4 reachable nodes and 3 noncyclic relations: %+v", graph)
	}
	base.Graph = graph
	hasCallSite := false
	for _, loc := range codeNodeLocations(base) {
		if loc.Line == 9 {
			hasCallSite = true
		}
		if loc.Line == 6 || loc.Line == 10 || loc.Line == 11 {
			t.Fatalf("unreachable/cyclic evidence leaked into source allowlist: %+v", loc)
		}
	}
	if !hasCallSite {
		t.Fatal("edge evidence must be available to the source endpoint")
	}
	idx.Nodes["auth"] = codeSpec{codeAnchor: codeAnchor{"api.go", "func deleted()"}, Title: "auth", Kind: "中间件"}
	missing := idx.graphFor(codeNode{Key: "route"}, []codeLinkSpec{link("route", "auth", "route_auth"), link("auth", "handler", "auth_handler")})
	if len(missing.Nodes) != 1 || len(missing.Edges) != 0 {
		t.Fatal("broken intermediate must disconnect descendants")
	}
}

func TestCodeGraphSharedDependency(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "api.go"), []byte("root\na\nb\nshared\ncall"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	idx := openCodeIndex(root)
	idx.Nodes = map[string]codeSpec{}
	for _, key := range []string{"root", "a", "b", "shared"} {
		idx.Nodes[key] = codeSpec{codeAnchor: codeAnchor{"api.go", key}, Title: key, Kind: "后端组件"}
	}
	base, _ := idx.node(idx.Nodes["root"])
	base.Key = "root"
	links := []codeLinkSpec{}
	for _, pair := range [][2]string{{"root", "a"}, {"root", "b"}, {"a", "shared"}, {"b", "shared"}} {
		links = append(links, codeLinkSpec{From: pair[0], To: pair[1], Label: "调用", Evidence: []codeAnchor{{"api.go", "call"}}})
	}
	graph := idx.graphFor(base, links)
	if len(graph.Nodes) != 4 || len(graph.Edges) != 4 {
		t.Fatalf("expected shared node and both inbound relations: %+v", graph)
	}
}
