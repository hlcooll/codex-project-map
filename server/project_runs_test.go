package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A real subprocess exercises the JSON-RPC transport without contacting a model.
func TestProjectRPCProcess(t *testing.T) {
	if os.Getenv("PROJECT_MAP_RPC_FIXTURE") != "1" {
		return
	}
	scenario := os.Getenv("PROJECT_MAP_RPC_SCENARIO")
	threadID := "fixture-thread"
	encoder := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	reply := func(id any, result any) { _ = encoder.Encode(map[string]any{"id": id, "result": result}) }
	notify := func(method string, params any) {
		_ = encoder.Encode(map[string]any{"method": method, "params": params})
	}
	for scanner.Scan() {
		var msg struct {
			ID     any                        `json:"id"`
			Method string                     `json:"method"`
			Params map[string]json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			os.Exit(2)
		}
		if logPath := os.Getenv("PROJECT_MAP_RPC_RECORD"); logPath != "" {
			f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			if err != nil {
				os.Exit(3)
			}
			f.Write(append(append([]byte{}, scanner.Bytes()...), byte(10)))
			f.Close()
		}
		text := func(k string) string { var s string; _ = json.Unmarshal(msg.Params[k], &s); return s }
		switch msg.Method {
		case "initialize":
			reply(msg.ID, map[string]any{"userAgent": "fixture"})
		case "initialized":
		case "thread/start", "thread/resume":
			if scenario == "disconnect" {
				os.Exit(0)
			}
			expected := "read-only"
			if scenario == "execution" {
				expected = "workspace-write"
			}
			if text("sandbox") != expected {
				_ = encoder.Encode(map[string]any{"id": msg.ID, "error": map[string]any{"code": -1, "message": "wrong permission scope"}})
				continue
			}
			if scenario == "context" {
				cwd, _ := os.Getwd()
				if text("cwd") != cwd {
					_ = encoder.Encode(map[string]any{"id": msg.ID, "error": map[string]any{"code": -1, "message": "process and thread cwd differ"}})
					continue
				}
				if msg.Method == "thread/resume" {
					threadID = text("threadId")
				} else {
					threadID = fmt.Sprintf("fixture-%d", os.Getpid())
				}
			}
			status := "idle"
			if scenario == "already-active" {
				status = "active"
			}
			reply(msg.ID, map[string]any{"thread": map[string]any{"id": threadID, "status": map[string]any{"type": status}}})
			if msg.Method == "thread/resume" && scenario == "normal" {
				notify("turn/completed", map[string]any{"threadId": threadID, "turn": map[string]any{"id": "old-turn", "status": "completed"}})
			}
		case "turn/start":
			if scenario == "start-fails" {
				_ = encoder.Encode(map[string]any{"id": msg.ID, "error": map[string]any{"code": -1, "message": "fixture failure"}})
				continue
			}
			notify("turn/completed", map[string]any{"threadId": threadID, "turn": map[string]any{"id": "old-turn", "status": "completed"}})
			if scenario == "slow" {
				reply(msg.ID, map[string]any{"turn": map[string]any{"id": "new-turn"}})
				continue
			}
			doc := samplePlan()
			raw, _ := json.Marshal(doc)
			text := string(raw)
			if scenario == "full-analysis" || scenario == "analysis-second-fails" || scenario == "context" {
				var input []struct {
					Text string `json:"text"`
				}
				_ = json.Unmarshal(msg.Params["input"], &input)
				prompt := ""
				if len(input) > 0 {
					prompt = input[0].Text
				}
				matches := regexp.MustCompile(`\n文件：([^\n]+)（模块 `).FindAllStringSubmatch(prompt, -1)
				result := analysisReply{planDocument: planDocument{Summary: "fixture batch", Kind: "Go", Stack: []string{"Go"}, Nodes: []planNode{}, Edges: []planEdge{}, Questions: []string{}}, ReviewedFiles: []string{}}
				for _, match := range matches {
					file := match[1]
					result.ReviewedFiles = append(result.ReviewedFiles, file)
					result.Nodes = append(result.Nodes, planNode{ID: "file-" + checksum(file), Title: file, Kind: "file", Description: "source", State: "existing", Evidence: []planEvidence{{File: file}}, Acceptance: []string{}})
				}
				encoded, _ := json.Marshal(result)
				text = string(encoded)
				if scenario == "analysis-second-fails" && strings.Contains(prompt, `"title":"file-00.go"`) {
					text = "invalid second batch"
				}
			}
			if scenario == "invalid-plan" {
				text = "Unstructured final answer"
			}
			if scenario == "execution" {
				text = "任务已执行，验收仍需要用户核对。"
			}
			if scenario == "context" {
				for i, cwd := range []string{"", os.Getenv("PROJECT_MAP_TARGET")} {
					notify("item/completed", map[string]any{"threadId": threadID, "turnId": "new-turn", "completedAtMs": time.Now().UnixMilli(), "item": map[string]any{"id": fmt.Sprintf("read-%d", i), "type": "commandExecution", "cwd": cwd, "command": "cat main.go", "status": "completed", "exitCode": 0, "commandActions": []any{map[string]any{"type": "read", "path": "main.go"}}}})
				}
			}
			at := time.Now().UnixMilli()
			notify("item/completed", map[string]any{"threadId": threadID, "turnId": "old-turn", "completedAtMs": at, "item": map[string]any{"id": "old-message", "type": "agentMessage", "text": "WRONG OLD TURN"}})
			notify("item/completed", map[string]any{"threadId": threadID, "turnId": "new-turn", "completedAtMs": at, "item": map[string]any{"id": "final-message", "type": "agentMessage", "phase": "final_answer", "text": text}})
			notify("turn/completed", map[string]any{"threadId": threadID, "turn": map[string]any{"id": "new-turn", "status": "completed"}})
			// Deliberately deliver current items and terminal notification before the response.
			reply(msg.ID, map[string]any{"turn": map[string]any{"id": "new-turn"}})
		case "turn/interrupt":
			reply(msg.ID, map[string]any{})
			notify("turn/completed", map[string]any{"threadId": threadID, "turn": map[string]any{"id": "new-turn", "status": "interrupted"}})
		default:
			reply(msg.ID, map[string]any{})
		}
	}
	os.Exit(0)
}
func plannerFixture(t *testing.T, scenario string) (*projectPlanner, project) {
	t.Helper()
	root := canonicalCwd(t.TempDir())
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {\n println(1)\n}\n"), 0600)
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "workbench.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	registry, err := newRegistry(db, root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := newProjectPlanner(db, registry, os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	p.skillRoot = t.TempDir()
	p.analysisCwd = canonicalCwd(t.TempDir())
	p.openRPC = func(ctx context.Context, _ string, cwd string, notify func(rpcEnvelope)) (*codexRPC, error) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestProjectRPCProcess$", "--")
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(), "PROJECT_MAP_RPC_FIXTURE=1", "PROJECT_MAP_RPC_SCENARIO="+scenario)
		return openCodexCommand(ctx, cmd, notify)
	}
	t.Cleanup(p.shutdown)
	item, err := registry.project("")
	if err != nil {
		t.Fatal(err)
	}
	return p, item
}

func TestProjectShutdownInterruptsWorkersAndRejectsNewRuns(t *testing.T) {
	p, item := plannerFixture(t, "slow")
	url := "/api/projects/" + item.ID + "/runs"
	w := plannerRequest(p, "POST", url, `{"role":"plan","prompt":"inspect"}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	p.shutdown()
	run := waitProjectRun(t, p, item.ID)
	if run.Status != "interrupted" {
		t.Fatalf("shutdown left run %s", run.Status)
	}
	if w = plannerRequest(p, "POST", url, `{"role":"plan","prompt":"inspect"}`); w.Code != 503 {
		t.Fatal("shutdown accepted another run", w.Code)
	}
}
func plannerRequest(p *projectPlanner, method, url, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	p.register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(method, url, strings.NewReader(body)))
	return w
}
func waitProjectRun(t *testing.T, p *projectPlanner, id string) projectRun {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		runs, err := p.runs(id)
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) > 0 && runs[0].Status != "starting" && runs[0].Status != "running" {
			p.mu.Lock()
			_, busy := p.active[id]
			p.mu.Unlock()
			if !busy {
				return runs[0]
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("run never settled")
	return projectRun{}
}
func TestProjectPlanningPersistsVersionAndReusesItsThread(t *testing.T) {
	p, item := plannerFixture(t, "normal")
	url := "/api/projects/" + item.ID + "/runs"
	for i := 1; i <= 2; i++ {
		w := plannerRequest(p, "POST", url, fmt.Sprintf(`{"role":"plan","prompt":"inspect","requestId":"req-%d"}`, i))
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		run := waitProjectRun(t, p, item.ID)
		if run.Status != "completed" || run.ThreadID != "fixture-thread" {
			t.Fatalf("unexpected run %+v", run)
		}
		doc, revision, _, err := p.document(item)
		if err != nil || revision != i || doc.Nodes[0].Title != "入口" {
			t.Fatalf("document mismatch: %d %+v %v", revision, doc, err)
		}
	}
	var old int
	_ = p.db.QueryRow(`SELECT count(*) FROM project_run_events WHERE item_id LIKE '%old-message'`).Scan(&old)
	if old != 0 {
		t.Fatal("previous turn contaminated current run")
	}
	w := plannerRequest(p, "POST", url, `{"role":"plan","prompt":"inspect","requestId":"req-2"}`)
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	runs, _ := p.runs(item.ID)
	if len(runs) != 2 {
		t.Fatal("idempotent request created another run")
	}
}
func TestProjectPlanningFailuresDoNotCreateDocuments(t *testing.T) {
	for _, scenario := range []string{"invalid-plan", "disconnect", "start-fails", "already-active"} {
		t.Run(scenario, func(t *testing.T) {
			p, item := plannerFixture(t, scenario)
			w := plannerRequest(p, "POST", "/api/projects/"+item.ID+"/runs", `{"role":"plan","prompt":"inspect"}`)
			if w.Code != 202 {
				t.Fatal(w.Code)
			}
			run := waitProjectRun(t, p, item.ID)
			if run.Status != "failed" {
				t.Fatalf("expected failure: %+v", run)
			}
			_, revision, _, _ := p.document(item)
			if revision != 0 {
				t.Fatal("failure overwrote document")
			}
			if scenario == "start-fails" {
				var n int
				_ = p.db.QueryRow(`SELECT count(*) FROM analysis_heads`).Scan(&n)
				if n != 0 {
					t.Fatal("failed turn changed planner head")
				}
			}
		})
	}
}
func TestProjectRunStopIsolationAndBusyGuard(t *testing.T) {
	p, item := plannerFixture(t, "slow")
	url := "/api/projects/" + item.ID + "/runs"
	w := plannerRequest(p, "POST", url, `{"role":"plan","prompt":"inspect"}`)
	var run projectRun
	_ = json.Unmarshal(w.Body.Bytes(), &run)
	if w = plannerRequest(p, "POST", url, `{"role":"plan","prompt":"second"}`); w.Code != 409 {
		t.Fatal("parallel task not rejected")
	}
	if w = plannerRequest(p, "POST", "/api/projects/other/runs/"+run.ID+"/interrupt", ""); w.Code != 409 {
		t.Fatal("foreign stop accepted")
	}
	if w = plannerRequest(p, "POST", url+"/"+run.ID+"/interrupt", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	ended := waitProjectRun(t, p, item.ID)
	if ended.Status != "interrupted" {
		t.Fatal(ended.Status)
	}
	_, revision, _, _ := p.document(item)
	if revision != 0 {
		t.Fatal("stopped run wrote document")
	}
}
func TestExecutionScopeAndUnknownNode(t *testing.T) {
	p, item := plannerFixture(t, "execution")
	url := "/api/projects/" + item.ID + "/runs"
	if w := plannerRequest(p, "POST", url, `{"role":"execute","nodeId":"missing","prompt":"test"}`); w.Code != 409 {
		t.Fatal("unknown node accepted")
	}
	doc, _, _, _ := p.document(item)
	node := doc.Nodes[0]
	w := plannerRequest(p, "POST", url, fmt.Sprintf(`{"role":"execute","nodeId":%q,"prompt":"test"}`, node.ID))
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	run := waitProjectRun(t, p, item.ID)
	if run.Status != "completed" {
		t.Fatal(run.Error)
	}
	if run.Context == nil || run.Context.Owner != "project" || run.Context.Cwd != item.Root || run.Context.Skill != nil {
		t.Fatal("execution moved into analyzer context", run.Context)
	}
	_, revision, _, _ := p.document(item)
	if revision != 0 {
		t.Fatal("execution silently changed plan")
	}
}
