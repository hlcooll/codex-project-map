package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func analysisFixtureFiles(t *testing.T, item project) {
	t.Helper()
	for i := 0; i < 13; i++ {
		if err := os.WriteFile(filepath.Join(item.Root, fmt.Sprintf("file-%02d.go", i)), []byte(fmt.Sprintf("package main\n// feature %d\n", i)), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func TestFullProjectAnalysisPersistsEveryBatchAndResumesChanges(t *testing.T) {
	p, item := plannerFixture(t, "full-analysis")
	analysisFixtureFiles(t, item)
	url := "/api/projects/" + item.ID + "/runs"
	w := plannerRequest(p, "POST", url, `{"role":"plan","analysis":"full","prompt":"inspect all","requestId":"full-one"}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	run := waitProjectRun(t, p, item.ID)
	if run.Status != "completed" {
		t.Fatal(run.Error)
	}
	a, err := p.analysis(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	view := a.view()
	if view.Total != 14 || view.Reviewed != 14 || view.Status != "complete" {
		t.Fatalf("bad coverage %+v", view)
	}
	doc, revision, _, err := p.document(item)
	if err != nil || revision != 3 || len(doc.Nodes) < 14 {
		t.Fatalf("batches were not accumulated rev=%d nodes=%d err=%v", revision, len(doc.Nodes), err)
	}
	if err = os.WriteFile(filepath.Join(item.Root, "file-00.go"), []byte("package main\n// changed feature\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = p.refreshAnalysis(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	a, _ = p.analysis(item.ID)
	if a.view().Reviewed != 13 || a.view().Integrated {
		t.Fatal("stale file reported current")
	}
	w = plannerRequest(p, "POST", url, `{"role":"plan","analysis":"full","prompt":"continue","requestId":"full-two"}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	run = waitProjectRun(t, p, item.ID)
	if run.Status != "completed" {
		t.Fatal(run.Error)
	}
	_, revision, _, _ = p.document(item)
	if revision != 5 {
		t.Fatalf("resume reprocessed completed files, revision %d", revision)
	}
	a, _ = p.analysis(item.ID)
	if a.view().Status != "complete" {
		t.Fatal(a.view())
	}
}
func TestAnalysisFailureKeepsEarlierBatches(t *testing.T) {
	p, item := plannerFixture(t, "analysis-second-fails")
	analysisFixtureFiles(t, item)
	w := plannerRequest(p, "POST", "/api/projects/"+item.ID+"/runs", `{"role":"plan","analysis":"full","prompt":"inspect"}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	run := waitProjectRun(t, p, item.ID)
	if run.Status != "failed" {
		t.Fatal(run.Status)
	}
	a, _ := p.analysis(item.ID)
	_, revision, _, err := p.document(item)
	if err != nil || revision != 1 || a.view().Reviewed != 12 {
		t.Fatalf("lost successful batch: revision %d coverage %+v err %v", revision, a.view(), err)
	}
}
func TestAnalysisRejectsUnobservedAndForeignFiles(t *testing.T) {
	p, item := plannerFixture(t, "normal")
	a, err := p.refreshAnalysis(context.Background(), item)
	if err != nil {
		t.Fatal(err)
	}
	doc := samplePlan()
	reply := analysisReply{planDocument: doc, ReviewedFiles: []string{"main.go"}}
	raw, _ := json.Marshal(reply)
	_, next, err := applyAnalysisReply(item, doc, a, a.Inventory.Files, turnOutput{Reply: string(raw), ReadFiles: map[string]bool{}}, map[string]bool{})
	if err != nil || next.view().Reviewed != 0 {
		t.Fatalf("unobserved file counted: %v", err)
	}
	reply.ReviewedFiles = []string{"../private.go"}
	raw, _ = json.Marshal(reply)
	if _, _, err = applyAnalysisReply(item, doc, a, a.Inventory.Files, turnOutput{Reply: string(raw)}, map[string]bool{}); err == nil {
		t.Fatal("foreign coverage accepted")
	}
}
func TestAnalysisCommitRollbackDoesNotAdvanceCoverage(t *testing.T) {
	p, item := plannerFixture(t, "normal")
	a, err := p.refreshAnalysis(context.Background(), item)
	if err != nil {
		t.Fatal(err)
	}
	run := projectRun{ID: "batch-rollback", Role: "plan", Status: "running", CreatedAt: time.Now()}
	if err = p.saveRun(item.ID, &run); err != nil {
		t.Fatal(err)
	}
	_, err = p.db.Exec(`CREATE TRIGGER reject_batch BEFORE UPDATE ON project_analysis BEGIN SELECT RAISE(ABORT,'fixture failure'); END;`)
	if err != nil {
		t.Fatal(err)
	}
	a.Reviewed["main.go"] = a.Inventory.Files[0].Digest
	doc := samplePlan()
	if err = p.commitAnalysis(context.Background(), item.ID, &run, &doc, a, false); err == nil {
		t.Fatal("expected transaction failure")
	}
	a, _ = p.analysis(item.ID)
	if a.view().Reviewed != 0 {
		t.Fatal("failed transaction advanced coverage")
	}
	var n int
	_ = p.db.QueryRow(`SELECT count(*) FROM plan_versions`).Scan(&n)
	if n != 0 {
		t.Fatal("failed transaction persisted document")
	}
}
func TestProjectSeedImportsUIAndMultiHopManifestWithoutClaimingAnalysis(t *testing.T) {
	p, item := plannerFixture(t, "normal")
	ui := `{"groups":[{"title":"首页","screens":[{"title":"首页","source":"main.go"},{"title":"记录","source":"main.go"}]}]}`
	manifest := `{"features":{"首页/记录":["api"]},"nodes":{"api":{"title":"保存接口","kind":"后端接口","file":"main.go","match":"func main()","links":[{"from":"api","to":"auth","label":"鉴权","evidence":[{"file":"main.go","match":"func main()"}]},{"from":"auth","to":"store","label":"保存","evidence":[{"file":"main.go","match":"println(1)"}]}]},"auth":{"title":"鉴权中间件","kind":"中间件","file":"main.go","match":"func main()"},"store":{"title":"数据存储","kind":"数据存储","file":"main.go","match":"println(1)"}}}`
	_ = os.WriteFile(filepath.Join(item.Root, appUIConfig), []byte(ui), 0600)
	_ = os.WriteFile(filepath.Join(item.Root, appCodeConfig), []byte(manifest), 0600)
	a, err := p.refreshAnalysis(context.Background(), item)
	if err != nil {
		t.Fatal(err)
	}
	doc, _, _, err := p.document(item)
	if err != nil {
		t.Fatal(err)
	}
	if a.view().Reviewed != 0 {
		t.Fatal("manifest import falsely counted as AI analysis")
	}
	if len(doc.Edges) != 4 {
		t.Fatalf("missing multi-hop relationships: %+v", doc.Edges)
	}
	found := false
	for _, e := range doc.Edges {
		found = found || (e.From == "native-auth" && e.To == "native-store")
	}
	if !found {
		t.Fatal("middleware storage chain absent")
	}
	w := plannerRequest(p, "GET", "/api/projects/"+item.ID+"/plan-source?edge=0&evidence=0", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "main.go") {
		t.Fatal("edge evidence not readable", w.Code, w.Body.String())
	}
	// An old numeric position must not override the stable relation/evidence key.
	query := url.Values{"from": {"native-auth"}, "to": {"native-store"}, "label": {"保存"}, "edge": {"999"}, "evidence": {"99"}, "proofFile": {"main.go"}, "proofMatch": {"println(1)"}}
	w = plannerRequest(p, "GET", "/api/projects/"+item.ID+"/plan-source?"+query.Encode(), "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "println(1)") {
		t.Fatal("stable relation evidence not resolved", w.Code, w.Body.String())
	}
	query.Set("proofMatch", "removed anchor")
	w = plannerRequest(p, "GET", "/api/projects/"+item.ID+"/plan-source?"+query.Encode(), "")
	if w.Code != 404 {
		t.Fatal("removed evidence fell back to a different source", w.Code)
	}
}

func TestAnalysisSparseNodeDoesNotErasePreviousModuleEvidence(t *testing.T) {
	base := samplePlan()
	base.Nodes[0].Category = "后端组件"
	base.Nodes[0].Description = "已有完整说明"
	update := planDocument{Nodes: []planNode{{ID: "main", Title: "入口", Kind: "feature", State: "unknown", Evidence: []planEvidence{}, Acceptance: []string{}}}}
	merged, err := mergeProjectDocuments(base, update, "codex")
	if err != nil {
		t.Fatal(err)
	}
	node := merged.Nodes[0]
	if node.Description != "已有完整说明" || len(node.Evidence) != 1 || len(node.Acceptance) != 1 || node.State != "existing" {
		t.Fatalf("sparse update erased earlier result %+v", node)
	}
	update.Nodes[0].Evidence = []planEvidence{{File: "other.go", Match: "func other()"}}
	merged, err = mergeProjectDocuments(merged, update, "codex")
	if err != nil || len(merged.Nodes[0].Evidence) != 2 {
		t.Fatalf("cross-module source was lost: %+v %v", merged.Nodes, err)
	}
}
func TestCancelledRunCannotPersistActiveProgress(t *testing.T) {
	p, item := plannerFixture(t, "normal")
	run := projectRun{ID: "cancel-progress", Role: "plan", Status: "starting", CreatedAt: time.Now()}
	if err := p.saveRun(item.ID, &run); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run.Status = "running"
	if err := p.saveActiveRun(ctx, item.ID, &run); err == nil {
		t.Fatal("cancelled progress was written")
	}
	runs, _ := p.runs(item.ID)
	if runs[0].Status != "starting" {
		t.Fatal("cancelled writer overwrote persisted state")
	}
}

func TestTruncatedInventoryCannotClaimIntegrated(t *testing.T) {
	a := analysisRecord{Inventory: projectInventory{Fingerprint: "same", Truncated: true, Files: []inventoryFile{{Path: "main.go", Digest: "v"}}}, Reviewed: map[string]string{"main.go": "v"}, Integrated: "same"}
	v := a.view()
	if v.Integrated || v.Status == "complete" {
		t.Fatalf("truncated scan claimed complete integration: %+v", v)
	}
}
