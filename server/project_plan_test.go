package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func samplePlan() planDocument {
	return planDocument{Summary: "fixture", Kind: "Go 服务", Stack: []string{"Go"}, Nodes: []planNode{{ID: "main", Title: "入口", Kind: "feature", State: "existing", Description: "启动服务", Evidence: []planEvidence{{File: "main.go", Match: "func main()"}}, Acceptance: []string{"运行测试"}}}, Edges: []planEdge{}, Questions: []string{}}
}
func TestPlanEvidenceIsRootBoundAndDetectsSourceDrift(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {\n println(1)\n}\n"), 0600)
	doc, err := validatePlan(root, samplePlan())
	if err != nil || !doc.Nodes[0].Evidence[0].Valid || doc.Nodes[0].Evidence[0].Line != 2 {
		t.Fatalf("bad valid source: %+v %v", doc, err)
	}
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {\n println(2)\n}\n"), 0600)
	changed, err := validatePlan(root, doc)
	if err != nil || changed.Nodes[0].Evidence[0].Valid {
		t.Fatal("changed source must invalidate old evidence")
	}
	dir, _ := os.OpenRoot(root)
	defer dir.Close()
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(outside, "private.go"), []byte("package private"), 0600)
	_ = os.Symlink(filepath.Join(outside, "private.go"), filepath.Join(root, "link.go"))
	for _, file := range []string{"../outside.go", "/etc/passwd", ".env", "a/../main.go", "link.go"} {
		e := validatePlanEvidence(dir, planEvidence{File: file})
		if e.Valid {
			t.Fatalf("outside/hidden file became evidence: %s", file)
		}
	}
}
func TestPlanRejectsBrokenReferencesAndInvalidJSON(t *testing.T) {
	doc := samplePlan()
	doc.Nodes = append(doc.Nodes, doc.Nodes[0])
	if _, err := validatePlan(t.TempDir(), doc); err == nil {
		t.Fatal("duplicate ids accepted")
	}
	doc = samplePlan()
	doc.Edges = []planEdge{{From: "main", To: "missing", Label: "调用"}}
	if _, err := validatePlan(t.TempDir(), doc); err == nil {
		t.Fatal("broken edge accepted")
	}
	doc = samplePlan()
	doc.Nodes = append(doc.Nodes, planNode{ID: "auth", Title: "auth", Kind: "component", State: "unknown"})
	doc.Edges = []planEdge{{From: "main", To: "auth", Label: "调用", Scopes: []string{"missing-api"}}}
	if _, err := validatePlan(t.TempDir(), doc); err == nil {
		t.Fatal("nonexistent route scope accepted")
	}
	if _, err := parsePlanReply("I am done"); err == nil {
		t.Fatal("unstructured answer accepted")
	}
}
func TestPlanCompletionIsAtomicAndCancellationDoesNotCommit(t *testing.T) {
	p, item := plannerFixture(t, "normal")
	run := projectRun{ID: "atomic", Role: "plan", Status: "running", CreatedAt: time.Now()}
	if err := p.saveRun(item.ID, &run); err != nil {
		t.Fatal(err)
	}
	_, err := p.db.Exec(`CREATE TRIGGER reject_success BEFORE UPDATE ON project_runs WHEN NEW.status='completed' BEGIN SELECT RAISE(ABORT,'fixture disk failure'); END;`)
	if err != nil {
		t.Fatal(err)
	}
	doc := samplePlan()
	if err = p.complete(context.Background(), item.ID, &run, &doc); err == nil {
		t.Fatal("expected storage failure")
	}
	var n int
	_ = p.db.QueryRow(`SELECT count(*) FROM plan_versions`).Scan(&n)
	if n != 0 {
		t.Fatal("failed completion persisted a document")
	}
	_, _ = p.db.Exec(`DROP TRIGGER reject_success`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = p.complete(ctx, item.ID, &run, &doc); err == nil {
		t.Fatal("cancelled run committed")
	}
	_ = p.db.QueryRow(`SELECT count(*) FROM plan_versions`).Scan(&n)
	if n != 0 {
		t.Fatal("cancelled completion persisted a document")
	}
}

func TestPlanRetainsLongExistingSQLSourceAnchor(t *testing.T) {
	root := t.TempDir()
	statement := "const schema = `CREATE TABLE tasks (" + strings.Repeat("column_name TEXT,", 45) + "last_column TEXT);`"
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"+statement+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	doc := samplePlan()
	doc.Nodes[0].Evidence[0].Match = statement
	got, err := validatePlan(root, doc)
	if err != nil || !got.Nodes[0].Evidence[0].Valid {
		t.Fatalf("existing long anchor rejected: %v", err)
	}
}
