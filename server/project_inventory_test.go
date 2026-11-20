package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestScanProjectInventoryGroupsAndSortsFiles(t *testing.T) {
	root := t.TempDir()
	writeInventoryTestFile(t, root, "main.go", "package main\n")
	writeInventoryTestFile(t, root, "src/backend/handler.go", "package backend\n")
	writeInventoryTestFile(t, root, "src/backend/deep/nested/trace.go", "package nested\n")
	writeInventoryTestFile(t, root, "src/ui/app.ts", "export const app = true\n")
	writeInventoryTestFile(t, root, "README.md", "# Demo\n")
	writeInventoryTestFile(t, root, "docs/guide.md", "# Guide\n")
	writeInventoryTestFile(t, root, "demos/demo.tsx", "export default function Demo() { return null }\n")

	inventory, err := scanProjectInventory(context.Background(), root)
	if err != nil {
		t.Fatalf("scanProjectInventory: %v", err)
	}
	gotPaths := inventoryPaths(inventory.Files)
	wantPaths := []string{
		"README.md", "demos/demo.tsx", "docs/guide.md", "main.go",
		"src/backend/deep/nested/trace.go", "src/backend/handler.go", "src/ui/app.ts",
	}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("paths = %#v, want %#v", gotPaths, wantPaths)
	}
	moduleByPath := map[string]string{}
	for _, file := range inventory.Files {
		moduleByPath[file.Path] = file.ModuleID
	}
	if moduleByPath["main.go"] != moduleByPath["README.md"] {
		t.Fatal("root files did not share the root module")
	}
	if moduleByPath["src/backend/handler.go"] != moduleByPath["src/backend/deep/nested/trace.go"] {
		t.Fatal("src/backend files did not share a module")
	}
	if moduleByPath["src/ui/app.ts"] == moduleByPath["src/backend/handler.go"] {
		t.Fatal("src second-level containers were not split")
	}
	if inventory.Modules[len(inventory.Modules)-1].Path != "src/ui" {
		t.Fatalf("modules are not sorted: %#v", inventory.Modules)
	}
}

func TestScanProjectInventoryExclusionsAndCredentials(t *testing.T) {
	root := t.TempDir()
	writeInventoryTestFile(t, root, "keep.go", "package keep\n")
	writeInventoryTestFile(t, root, "credential_handler.go", "package auth\n")
	writeInventoryTestFile(t, root, "node_modules/pkg/index.js", "module.exports = 1\n")
	writeInventoryTestFile(t, root, ".hidden/code.go", "package hidden\n")
	writeInventoryTestFile(t, root, ".env", "TOKEN=secret\n")
	writeInventoryTestFile(t, root, ".ssh/id_rsa", "private\n")
	writeInventoryTestFile(t, root, "docs/guide.md", "# Guide\n")
	writeInventoryTestFile(t, root, "demos/example.tsx", "export default 1\n")
	writeInventoryTestFile(t, root, "output/app.js", "generated\n")
	writeInventoryTestFile(t, root, "coverage/app.go", "package coverage\n")
	writeInventoryTestFile(t, root, ".build/cache.go", "package cache\n")

	inventory, err := scanProjectInventory(context.Background(), root)
	if err != nil {
		t.Fatalf("scanProjectInventory: %v", err)
	}
	if got := inventoryPaths(inventory.Files); !reflect.DeepEqual(got, []string{"credential_handler.go", "demos/example.tsx", "docs/guide.md", "keep.go"}) {
		t.Fatalf("kept files = %#v", got)
	}
	credential := inventoryExclusionCount(inventory, "凭据文件")
	if credential < 2 {
		t.Fatalf("credential exclusions = %d, want at least 2 (.env and .ssh)", credential)
	}
	if inventoryExclusionCount(inventory, "生成、缓存或依赖目录") < 4 {
		t.Fatalf("generated exclusions = %#v", inventory.Excluded)
	}
	assertInventoryExclusionsSorted(t, inventory.Excluded)
}

func TestScanProjectInventoryRejectsOutOfRootSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "outside.go")
	if err := os.WriteFile(outsideFile, []byte("package outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(root, "linked.go")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	writeInventoryTestFile(t, root, "inside.go", "package inside\n")

	inventory, err := scanProjectInventory(context.Background(), root)
	if err != nil {
		t.Fatalf("scanProjectInventory: %v", err)
	}
	if got := inventoryPaths(inventory.Files); !reflect.DeepEqual(got, []string{"inside.go"}) {
		t.Fatalf("files = %#v", got)
	}
	if inventoryExclusionCount(inventory, "符号链接") != 1 {
		t.Fatalf("excluded = %#v", inventory.Excluded)
	}
}

func TestScanProjectInventoryHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	writeInventoryTestFile(t, root, "main.go", "package main\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := scanProjectInventory(ctx, root)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestScanProjectInventoryFingerprintChangesWithContentOnly(t *testing.T) {
	root := t.TempDir()
	file := writeInventoryTestFile(t, root, "src/backend/main.go", "package backend\n")
	first, err := scanProjectInventory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	firstPath := first.Files[0].Path
	firstModule := first.Files[0].ModuleID

	if err := os.WriteFile(file, []byte("package backend // changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := scanProjectInventory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint == second.Fingerprint {
		t.Fatal("fingerprint did not change")
	}
	if first.Files[0].Path != firstPath || second.Files[0].Path != firstPath {
		t.Fatal("path changed with content")
	}
	if first.Files[0].ModuleID != firstModule || second.Files[0].ModuleID != firstModule {
		t.Fatal("module ID changed with content")
	}
}

func TestScanProjectInventoryLimitsAndWarnings(t *testing.T) {
	root := t.TempDir()
	writeInventoryTestFile(t, root, "a.go", "package a\n")
	writeInventoryTestFile(t, root, "b.go", "package b\n")
	writeInventoryTestFile(t, root, "c.go", "package c\n")

	inventory, err := scanProjectInventoryWithLimits(context.Background(), root, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.Truncated || len(inventory.Files) != 2 {
		t.Fatalf("file limit result = truncated %v files %d", inventory.Truncated, len(inventory.Files))
	}
	if len(inventory.Warnings) == 0 {
		t.Fatal("missing truncation warning")
	}

	inventory, err = scanProjectInventoryWithLimits(context.Background(), root, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.Truncated {
		t.Fatal("entry limit did not mark inventory truncated")
	}
}

func TestScanProjectInventoryLargeFileExcluded(t *testing.T) {
	root := t.TempDir()
	large := make([]byte, maxInventoryFileSize+1)
	for i := range large {
		large[i] = 'x'
	}
	if err := os.WriteFile(filepath.Join(root, "large.go"), large, 0o600); err != nil {
		t.Fatal(err)
	}

	inventory, err := scanProjectInventory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Files) != 0 {
		t.Fatalf("large file was included: %#v", inventory.Files)
	}
	found := false
	for _, warning := range inventory.Warnings {
		if warning == "文件超过 768 KiB，已排除：large.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %#v", inventory.Warnings)
	}
}

func TestScanProjectInventoryEmptyAndFingerprintStability(t *testing.T) {
	root := t.TempDir()
	inventory, err := scanProjectInventory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Files == nil || inventory.Modules == nil || inventory.Excluded == nil || inventory.Warnings == nil {
		t.Fatal("slice fields must be non-nil")
	}
	if inventory.ScannedAt.IsZero() || time.Since(inventory.ScannedAt) > time.Minute {
		t.Fatalf("unexpected ScannedAt: %v", inventory.ScannedAt)
	}
	if inventory.Fingerprint != checksum("") {
		t.Fatalf("fingerprint = %q", inventory.Fingerprint)
	}
	files := []inventoryFile{{Path: "z.go", Digest: "b"}, {Path: "a.go", Digest: "a"}}
	reordered := []inventoryFile{{Path: "a.go", Digest: "a"}, {Path: "z.go", Digest: "b"}}
	if inventoryFingerprint(files) != inventoryFingerprint(reordered) {
		t.Fatal("fingerprint should depend on sorted path/digest pairs")
	}
}

func writeInventoryTestFile(t *testing.T, root, name, contents string) string {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func inventoryPaths(files []inventoryFile) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	return paths
}

func inventoryExclusionCount(inventory projectInventory, reason string) int {
	for _, exclusion := range inventory.Excluded {
		if exclusion.Reason == reason {
			return exclusion.Count
		}
	}
	return 0
}

func assertInventoryExclusionsSorted(t *testing.T, exclusions []inventoryExclusion) {
	t.Helper()
	for i := 1; i < len(exclusions); i++ {
		if exclusions[i-1].Reason > exclusions[i].Reason {
			t.Fatalf("exclusions not sorted: %#v", exclusions)
		}
	}
}
