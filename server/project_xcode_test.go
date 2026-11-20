package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestXcodeConfigurationScansAndSourceAnchorsAgree(t *testing.T) {
	root := t.TempDir()
	files := []string{"Neural.xcodeproj/project.pbxproj", "Neural.xcodeproj/xcshareddata/xcschemes/Neural.xcscheme", "Neural.xcworkspace/contents.xcworkspacedata", "Config/App.xcconfig", "Neural/App.entitlements"}
	for _, file := range append(append([]string{}, files...), "previews/Demo.app/Contents/Resources/copy.swift", "SDK.framework/Headers/copy.h", "Build/main.swift") {
		target := filepath.Join(root, file)
		_ = os.MkdirAll(filepath.Dir(target), 0700)
		_ = os.WriteFile(target, []byte("// unique configuration anchor\n"), 0600)
	}
	inv, err := scanProjectInventory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Files) != len(files) {
		t.Fatalf("unexpected inventory %+v", inv.Files)
	}
	dir, _ := os.OpenRoot(root)
	defer dir.Close()
	for _, file := range files {
		found := false
		for _, f := range inv.Files {
			found = found || f.Path == file
		}
		if !found || !validatePlanEvidence(dir, planEvidence{File: file, Match: "unique configuration anchor"}).Valid {
			t.Fatal("configuration missing from scan or evidence", file)
		}
	}
}
