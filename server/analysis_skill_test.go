package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAnalysisContextMigratesAndKeepsTargetsSeparate(t *testing.T) {
	p, item := plannerFixture(t, "context")
	logPath := filepath.Join(t.TempDir(), "rpc.jsonl")
	p.openRPC = func(ctx context.Context, _ string, cwd string, notify func(rpcEnvelope)) (*codexRPC, error) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestProjectRPCProcess$", "--")
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(), "PROJECT_MAP_RPC_FIXTURE=1", "PROJECT_MAP_RPC_SCENARIO=context", "PROJECT_MAP_RPC_RECORD="+logPath, "PROJECT_MAP_TARGET="+item.Root)
		return openCodexCommand(ctx, cmd, notify)
	}
	_, err := p.db.Exec(`INSERT INTO plan_heads VALUES (?,?)`, item.ID, "legacy-target-thread")
	if err != nil {
		t.Fatal(err)
	}
	old := projectRun{ID: "old-run", Role: "plan", ThreadID: "legacy-target-thread", Status: "completed", CreatedAt: time.Now().Add(-time.Hour)}
	if err = p.saveRun(item.ID, &old); err != nil {
		t.Fatal(err)
	}
	start := func(projectID string) projectRun {
		t.Helper()
		w := plannerRequest(p, "POST", "/api/projects/"+projectID+"/runs", `{"role":"plan","analysis":"full","prompt":"inspect"}`)
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		run := waitProjectRun(t, p, projectID)
		if run.Status != "completed" {
			t.Fatal(run.Error)
		}
		return run
	}
	first := start(item.ID)
	if first.ThreadID == old.ThreadID || first.Context.Owner != "workbench" || first.Context.Cwd != p.analysisCwd || first.Context.TargetRoot != item.Root || first.Context.Skill.Version == "" {
		t.Fatalf("wrong ownership: %+v", first)
	}
	second := start(item.ID)
	if second.ThreadID != first.ThreadID {
		t.Fatal("same target/version did not resume")
	}
	// A second target must create its own conversation even with the same cwd/skill.
	otherRoot := canonicalCwd(t.TempDir())
	_ = os.WriteFile(filepath.Join(otherRoot, "main.go"), []byte("package main\n"), 0600)
	otherID := checksum(otherRoot)
	_, err = p.db.Exec(`INSERT INTO projects VALUES (?,?,?)`, otherID, otherRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	other := start(otherID)
	if other.ThreadID == first.ThreadID || other.Context.TargetRoot != otherRoot {
		t.Fatal("target conversations mixed")
	}
	// An old skill head must never resume under new instructions.
	_, _ = p.db.Exec(`UPDATE analysis_heads SET skill_key='old-version' WHERE project_id=?`, item.ID)
	a, _ := p.analysis(item.ID)
	a.SkillKey = "old-version"
	if err := p.saveAnalysis(context.Background(), item.ID, a); err != nil {
		t.Fatal(err)
	}
	_, beforeRevision, _, _ := p.document(item)
	third := start(item.ID)
	_, afterRevision, _, _ := p.document(item)
	if afterRevision-beforeRevision != 2 {
		t.Fatal("skill upgrade did not re-review files")
	}
	if third.ThreadID == first.ThreadID {
		t.Fatal("resumed a stale skill context")
	}
	w := plannerRequest(p, "GET", "/api/projects/"+item.ID+"/workbench", "")
	var state planState
	if json.Unmarshal(w.Body.Bytes(), &state) != nil {
		t.Fatal(w.Body.String())
	}
	if state.PlannerThreadID != third.ThreadID || state.Revision < 4 {
		t.Fatal("lost durable results")
	}
	foundOld := false
	for _, r := range state.Runs {
		if r.ID == old.ID {
			foundOld = r.Context.Owner == "project" && r.Context.Skill == nil && r.Context.Cwd == item.Root
		}
	}
	if !foundOld {
		t.Fatal("historical ownership was rewritten")
	}
	data, _ := os.ReadFile(logPath)
	starts, resumes, skills := 0, 0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var call struct {
			Method string
			Params map[string]json.RawMessage
		}
		if json.Unmarshal([]byte(line), &call) != nil {
			t.Fatal("bad fixture log")
		}
		if call.Method == "thread/start" || call.Method == "thread/resume" {
			var cwd, sandbox string
			json.Unmarshal(call.Params["cwd"], &cwd)
			json.Unmarshal(call.Params["sandbox"], &sandbox)
			if cwd != p.analysisCwd || sandbox != "read-only" {
				t.Fatal("wrong RPC ownership/permission")
			}
			if call.Method == "thread/start" {
				starts++
			} else {
				resumes++
			}
		}
		if call.Method == "turn/start" {
			var inputs []struct{ Type, Name, Path, Text string }
			json.Unmarshal(call.Params["input"], &inputs)
			if len(inputs) != 2 || inputs[1].Type != "skill" || inputs[1].Name != "project-analyzer" || inputs[1].Path != first.Context.Skill.Path || !strings.Contains(inputs[0].Text, "会话归属：工作台") {
				t.Fatal("skill not actually sent", string(line))
			}
			skills++
		}
	}
	if starts != 3 || resumes < 1 || skills < 5 {
		t.Fatal(starts, resumes, skills)
	}
	rows, err := p.db.Query(`SELECT item_id,data FROM project_run_events WHERE run_id=?`, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seenWrong, seenRight := false, false
	for rows.Next() {
		var id, raw string
		rows.Scan(&id, &raw)
		var stored storedProjectActivity
		json.Unmarshal([]byte(raw), &stored)
		if strings.HasSuffix(id, ":read-0") {
			seenWrong = true
			if len(stored.Event.Paths) != 0 {
				t.Fatal("workbench file counted as target", stored.Event.Paths)
			}
		}
		if strings.HasSuffix(id, ":read-1") {
			seenRight = true
			if len(stored.Event.Paths) != 1 || stored.Event.Paths[0] != "main.go" {
				t.Fatal("target read lost", stored.Event.Paths)
			}
		}
	}
	if !seenWrong || !seenRight {
		t.Fatal("missing provenance events")
	}
}

func TestPinnedSkillDetectsChangesAndRoutesAdapters(t *testing.T) {
	p, item := plannerFixture(t, "normal")
	a := &analysisRecord{Inventory: projectInventory{Files: []inventoryFile{{Path: "App.swift"}, {Path: "ui/Home.vue"}, {Path: "api/main.go"}}}}
	run := projectRun{}
	if err := p.pinAnalysis(item, &run, a); err != nil {
		t.Fatal(err)
	}
	if strings.Join(run.Context.Skill.Adapters, ",") != "swift,web,go" {
		t.Fatal(run.Context.Skill.Adapters)
	}
	text, err := pinnedSkillText(run.Context.Skill)
	if err != nil || !strings.Contains(text, ".pbxproj") || !strings.Contains(text, "Vue Router") {
		t.Fatal("missing activated references", err)
	}
	path := filepath.Join(filepath.Dir(run.Context.Skill.Path), "references", "swift.md")
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("changed during analysis"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = pinnedSkillText(run.Context.Skill); err == nil {
		t.Fatal("mutated skill remained active")
	}
	if err = p.pinAnalysis(item, &run, a); err == nil {
		t.Fatal("republished over tampered immutable version")
	}
}

func TestCapabilityGapsRequireReadEvidenceAndStayProjectLocal(t *testing.T) {
	p, item := plannerFixture(t, "full-analysis")
	a, err := p.refreshAnalysis(context.Background(), item)
	if err != nil {
		t.Fatal(err)
	}
	gap := analysisGap{Kind: "adapter", Summary: "入口适配待验证", Proposal: "增加针对入口的识别规则", Verification: "用两个独立样例验证入口枚举", Evidence: []planEvidence{{File: "main.go", Match: "func main()"}}, RunID: "forged", SkillVersion: "forged"}
	if got := mergeAnalysisGaps(item, a, []analysisGap{gap}, nil, nil); len(got) != 0 {
		t.Fatal("unread claim accepted")
	}
	a.Gaps = mergeAnalysisGaps(item, a, []analysisGap{gap, gap}, map[string]bool{"main.go": true}, nil)
	if len(a.Gaps) != 1 || a.Gaps[0].RunID != "" || a.Gaps[0].SkillVersion != "" || !a.Gaps[0].Evidence[0].Valid {
		t.Fatal(a.Gaps)
	}
	gap.Evidence[0].File = "../main.go"
	got := mergeAnalysisGaps(item, a, []analysisGap{gap}, map[string]bool{"../main.go": true}, nil)
	if len(got) != 1 {
		t.Fatal("foreign evidence accepted")
	}
	if err = p.saveAnalysis(context.Background(), item.ID, a); err != nil {
		t.Fatal(err)
	}
	restored, _ := p.analysis(item.ID)
	other, _ := p.analysis("unrelated")
	if other != nil || len(restored.view().Gaps) != 1 {
		t.Fatal("project gap archive leaked or lost")
	}
}

// Opt-in contract check against the installed Codex, using an isolated target
// and database. Ordinary tests never call a model or create a Codex chat.
func TestAnalysisSkillRealCodex(t *testing.T) {
	binary := os.Getenv("PROJECT_MAP_REAL_CODEX")
	if binary == "" {
		t.Skip("set PROJECT_MAP_REAL_CODEX for the local integration check")
	}
	p, item := plannerFixture(t, "normal")
	p.binary, p.openRPC = binary, openCodexRPC
	workbench, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	p.analysisCwd = canonicalCwd(workbench)
	item.Name = "analysis-skill-check"
	w := plannerRequest(p, "POST", "/api/projects/"+item.ID+"/runs", `{"role":"plan","analysis":"full","prompt":"这是隔离验证样例，只有 main.go。只分析此文件中的真实入口与输出语句，使用最少必要节点，不调查其他目录。没有真实适配缺口则 capabilityGaps 返回空数组。"}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var latest projectRun
	t.Cleanup(func() {
		p.shutdown()
		if latest.ThreadID == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		client, err := openCodexRPC(ctx, binary, p.analysisCwd, nil)
		if err != nil {
			t.Log("test chat cleanup:", err)
			return
		}
		defer client.close()
		if _, err = client.call(ctx, "thread/archive", map[string]any{"threadId": latest.ThreadID}, nil); err != nil {
			t.Log("test chat cleanup:", err)
		}
	})
	deadline, last := time.Now().Add(4*time.Minute), ""
	for time.Now().Before(deadline) {
		runs, err := p.runs(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) > 0 {
			latest = runs[0]
			if latest.Stage+latest.Status != last {
				last = latest.Stage + latest.Status
				t.Log(latest.Status, latest.Stage)
			}
			if latest.Status != "starting" && latest.Status != "running" {
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if latest.Status != "completed" {
		t.Fatalf("real analysis: %s %s", latest.Status, latest.Error)
	}
	a, err := p.analysis(item.ID)
	if err != nil || a.view().Status != "complete" || a.view().Reviewed != 1 {
		t.Fatal("coverage", a, err)
	}
	if latest.Context == nil || latest.Context.Cwd != p.analysisCwd || latest.Context.TargetRoot != item.Root || latest.Context.Skill == nil {
		t.Fatal("context", latest.Context)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := openCodexRPC(ctx, binary, p.analysisCwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	data, err := client.call(ctx, "thread/read", map[string]any{"threadId": latest.ThreadID, "includeTurns": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Thread struct {
			Cwd   string            `json:"cwd"`
			Turns []json.RawMessage `json:"turns"`
		} `json:"thread"`
	}
	if json.Unmarshal(data, &result) != nil || result.Thread.Cwd != p.analysisCwd || len(result.Thread.Turns) < 2 {
		t.Fatal("actual Codex chat ownership/turns mismatch")
	}
	t.Logf("verified real chat cwd=%s, skill=%s, target files=%d/%d, turns=%d", result.Thread.Cwd, latest.Context.Skill.Version, a.view().Reviewed, a.view().Total, len(result.Thread.Turns))
}
