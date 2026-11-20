package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodeIndexResolvesOnlyLocatedRelations(t *testing.T) {
	dir := t.TempDir()
	assets := map[string]string{
		"Screen.swift": "import SwiftUI\nstruct Screen: View {}\nfunc load() { request(\"/items\") }\n",
		"api.go":       "package api\nfunc register() { mux.Handle(\"GET /items\", auth(items)) }\nfunc items() {}\nfunc auth() {}\n",
		"secret.env":   "TOKEN=secret\n",
		appCodeConfig: `{
			"features": {"Group/Screen": ["view", "items", "missing", "wrong", "secret", "escape"]},
			"nodes": {
				"view": {"title":"Screen", "kind":"UI 页面", "file":"Screen.swift", "match":"struct Screen"},
				"items": {"title":"GET /items", "kind":"后端接口", "file":"api.go", "match":"func items", "evidence":[
				  {"file":"Screen.swift","match":"request(\"/items\")"}, {"file":"api.go","match":"mux.Handle"}], "uses":["auth","wrong"]},
				"missing": {"title":"Missing", "kind":"后端接口", "file":"api.go", "match":"func items", "evidence":[
				  {"file":"Screen.swift","match":"request(\"/deleted\")"}, {"file":"api.go","match":"mux.Handle"}]},
				"auth": {"title":"Middleware", "kind":"中间件", "file":"api.go", "match":"func auth"},
				"wrong": {"title":"Wrong", "kind":"中间件", "file":"api.go", "match":"func unknown"},
				"secret": {"title":"Secret", "kind":"客户端服务", "file":"secret.env"},
				"escape": {"title":"Escape", "kind":"客户端服务", "file":"../outside.swift"}
			}
		}`,
	}
	for file, content := range assets {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	idx := openCodeIndex(root)
	result := idx.forScreen("Group", appScreen{Title: "Screen", Source: "Screen.swift"})
	if len(result) != 2 || result[0].Line != 2 || len(result[1].Uses) != 1 || result[1].Uses[0].Line != 4 || len(result[1].Evidence) != 2 {
		t.Fatalf("expected exact source, API evidence, and one valid dependency; got %+v", result)
	}
	anchor := result[1].Evidence[0]
	start, snippet := codeSnippet(idx, anchor)
	if len(snippet) == 0 || snippet[anchor.Line-start] != `func load() { request("/items") }` {
		t.Fatalf("expected original evidence line in loaded source: %+v", snippet)
	}
	fallback := idx.forScreen("Other Group", appScreen{Title: "Screen", Source: "Screen.swift"})
	if len(fallback) != 1 || fallback[0].Line != 1 || fallback[0].Kind != "UI 页面" {
		t.Fatalf("expected file-only UI fallback, got %+v", fallback)
	}
}
