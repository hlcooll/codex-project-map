package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func activityFixture(t *testing.T) (string, string, *sql.DB, *sql.DB) {
	t.Helper()
	root := canonicalCwd(t.TempDir())
	home := t.TempDir()
	s, err := sql.Open("sqlite3", filepath.Join(home, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := sql.Open("sqlite3", filepath.Join(home, "thread_history_1.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); h.Close() })
	if _, err = s.Exec(`CREATE TABLE threads(id TEXT,cwd TEXT,name TEXT,title TEXT,rollout_path TEXT,updated_at INTEGER,archived INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err = h.Exec(`CREATE TABLE thread_turns(thread_id TEXT,turn_id TEXT,status TEXT,rollout_ordinal INTEGER); CREATE TABLE thread_items(thread_id TEXT,turn_id TEXT,item_id TEXT,item_type TEXT,created_at_ms INTEGER,started_at_ms INTEGER,completed_at_ms INTEGER,rollout_ordinal INTEGER,item_json TEXT)`); err != nil {
		t.Fatal(err)
	}
	return root, home, s, h
}
func fixtureItem(t *testing.T, h *sql.DB, thread, turn, id, kind string, when time.Time, payload any) {
	t.Helper()
	raw, _ := json.Marshal(payload)
	_, err := h.Exec(`INSERT INTO thread_items VALUES (?,?,?,?,?,NULL,NULL,?,?)`, thread, turn, id, kind, when.UnixMilli(), when.UnixMilli(), string(raw))
	if err != nil {
		t.Fatal(err)
	}
}
func TestActivityHistoryCrossProjectAndLifecycle(t *testing.T) {
	root, home, s, h := activityFixture(t)
	now := time.Now()
	old := now.Add(-48 * time.Hour)
	_, _ = s.Exec(`INSERT INTO threads VALUES ('old',?,'旧会话','','',?,0),('cross',?,'跨项目','','',?,0)`, root, old.Unix(), t.TempDir(), now.Unix())
	_, _ = h.Exec(`INSERT INTO thread_turns VALUES ('old','t1','completed',1),('cross','t2','inProgress',2)`)
	fixtureItem(t, h, "old", "t1", "m1", "userMessage", old, map[string]any{"content": []any{map[string]any{"type": "text", "text": "修改购物清单"}}})
	fixtureItem(t, h, "old", "t1", "c1", "commandExecution", old, map[string]any{"status": "inProgress", "command": "go test ./..."})
	fixtureItem(t, h, "cross", "t2", "c2", "fileChange", now, map[string]any{"status": "inProgress", "changes": []any{map[string]any{"path": filepath.Join(root, "api.go"), "diff": "@@ -1 +1 @@\n-old\n+new\n"}}})
	fixtureItem(t, h, "cross", "t2", "private", "userMessage", now, map[string]any{"content": "unrelated conversation"})
	first := observe(root, home)
	if first.Connection != "ready" || len(first.Sessions) != 2 || first.SessionCount != 1 {
		t.Fatalf("history and cross-project operation should be present: %+v", first)
	}
	statuses := map[string]string{}
	versions := map[string]string{}
	for _, e := range first.Events {
		statuses[e.ID] = e.Status
		versions[e.ID] = e.Version
		if e.Detail == "unrelated conversation" {
			t.Fatal("foreign unrelated dialogue leaked")
		}
	}
	if statuses[checksum("old:c1")] != "interrupted" || statuses[checksum("cross:c2")] != "running" {
		t.Fatal(statuses)
	}
	_, _ = h.Exec(`UPDATE thread_items SET item_json=replace(item_json,'inProgress','completed') WHERE item_id='c2'`)
	next := observe(root, home)
	if next.SessionCount != 0 {
		t.Fatal("foreign thread working elsewhere must not mark selected project busy")
	}
	for _, e := range next.Events {
		if e.ID == checksum("cross:c2") && e.Version == versions[e.ID] {
			t.Fatal("status transition must change version without changing event id")
		}
	}
}
func TestActivityMappingUsesChangedLinesNotWholeHunk(t *testing.T) {
	root := canonicalCwd(t.TempDir())
	source := "package api\nfunc first() {\n println(1)\n}\nfunc second() {\n println(2)\n}\n"
	_ = os.WriteFile(filepath.Join(root, "api.go"), []byte(source), 0600)
	_ = os.WriteFile(filepath.Join(root, appUIConfig), []byte(`{"groups":[{"title":"Home","screens":[{"title":"One"},{"title":"Two"}]}]}`), 0600)
	_ = os.WriteFile(filepath.Join(root, appCodeConfig), []byte(`{"features":{"Home/One":["one"],"Home/Two":["two"]},"nodes":{"one":{"title":"first","kind":"后端组件","file":"api.go","match":"func first()"},"two":{"title":"second","kind":"后端组件","file":"api.go","match":"func second()"}}}`), 0600)
	raw, _ := json.Marshal(map[string]any{"status": "completed", "changes": []any{map[string]any{"path": "api.go", "diff": "@@ -2,6 +2,6 @@\n func first() {\n  println(1)\n }\n func second() {\n- println(1)\n+ println(2)\n }\n"}}})
	e, ok := normalizeActivity(root, observedThread{id: "t", cwd: root, association: "workspace"}, observedItem{id: "e", kind: "fileChange", at: time.Now().UnixMilli(), raw: raw})
	if !ok {
		t.Fatal("missing change")
	}
	out := snapshot{Events: []activity{e}}
	attachActivityTargets(root, &out)
	if out.Events[0].Mapping != "exact" || len(out.Events[0].Targets) != 1 || out.Events[0].Targets[0].Key != "two" {
		t.Fatalf("unchanged first() must not light: %+v", out.Events[0])
	}
	out.Events[0].Targets = nil
	out.Events[0].At = time.Now().Add(-48 * time.Hour)
	attachActivityTargets(root, &out)
	if out.Events[0].Mapping != "file" {
		t.Fatal("historical line numbers cannot locate current functions")
	}
	out = snapshot{Events: []activity{{Kind: "write", At: time.Now(), Locations: []activityLocation{{Path: "api.go", Text: "// func first() is documented here"}}, Mapping: "unmapped"}}}
	attachActivityTargets(root, &out)
	if out.Events[0].Mapping == "exact" {
		t.Fatal("a comment mentioning a declaration is not a declaration edit")
	}
}
func TestActivityNoFalseSuccessAndNoUnprovenPaths(t *testing.T) {
	root := canonicalCwd(t.TempDir())
	t0 := observedThread{id: "t", cwd: root, association: "workspace"}
	raw := json.RawMessage(`{"status":"completed","command":"echo /some/private/file.go","exitCode":null}`)
	e, _ := normalizeActivity(root, t0, observedItem{id: "e", kind: "commandExecution", at: time.Now().UnixMilli(), raw: raw})
	if e.Status != "unknown" || len(e.Paths) > 0 {
		t.Fatalf("unknown exit code or command text is not success/path evidence: %+v", e)
	}
	for _, status := range []string{"failed", "interrupted", "cancelled"} {
		raw, _ = json.Marshal(map[string]any{"status": status, "exitCode": 0})
		e, _ = normalizeActivity(root, t0, observedItem{id: "e", kind: "commandExecution", at: time.Now().UnixMilli(), raw: raw})
		if e.Status == "completed" {
			t.Fatalf("zero exit code must not override %s", status)
		}
	}
}

func TestCrossProjectUnrelatedActivityDoesNotRefreshOldOperation(t *testing.T) {
	root, home, s, h := activityFixture(t)
	now := time.Now()
	_, _ = s.Exec(`INSERT INTO threads VALUES ('foreign',?,'foreign','','',?,0)`, t.TempDir(), now.Unix())
	_, _ = h.Exec(`INSERT INTO thread_turns VALUES ('foreign','turn','inProgress',1)`)
	fixtureItem(t, h, "foreign", "turn", "old", "fileChange", now.Add(-6*time.Minute), map[string]any{"status": "inProgress", "changes": []any{map[string]any{"path": filepath.Join(root, "api.go"), "diff": "@@ -1 +1 @@\n-a\n+b"}}})
	fixtureItem(t, h, "foreign", "turn", "elsewhere", "commandExecution", now, map[string]any{"status": "inProgress", "command": "sleep 10"})
	got := observe(root, home)
	if got.SessionCount != 0 || len(got.Events) != 1 || got.Events[0].Status != "stale" {
		t.Fatalf("unrelated operation must not refresh project: %+v", got)
	}
}

func TestActivityLineEvidenceRejectsDriftAndRepeatedText(t *testing.T) {
	loc := activityLocation{Line: 3, EndLine: 3, Text: " println(2)"}
	if verifiedActivityLines("write", loc, []string{"func first() {", " println(1)", "}", "func second() {", " println(2)", "}"}) {
		t.Fatal("shifted patch line must not identify first()")
	}
	if verifiedActivityLines("write", loc, []string{"func first() {", " println(2)", " println(2)", "}"}) {
		t.Fatal("repeated text is not a unique anchor")
	}
	loc = activityLocation{Line: 2, EndLine: 3, Text: " println(2)\n}"}
	if verifiedActivityLines("read", loc, []string{"func first() {", " println(1)", "}"}) {
		t.Fatal("read output must agree with current range")
	}
	if !verifiedActivityLines("read", loc, []string{"func second() {", " println(2)", "}"}) {
		t.Fatal("matching structured read output is evidence")
	}
}
func TestActivityJSONLFallback(t *testing.T) {
	root := canonicalCwd(t.TempDir())
	home := t.TempDir()
	dir := filepath.Join(home, "sessions")
	_ = os.MkdirAll(dir, 0700)
	records := []any{
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": "cli", "cwd": root}},
		map[string]any{"type": "event_msg", "timestamp": time.Now().Format(time.RFC3339Nano), "payload": map[string]any{"type": "task_started", "turn_id": "turn"}},
		map[string]any{"type": "response_item", "timestamp": time.Now().Format(time.RFC3339Nano), "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "请运行测试"}}}},
		map[string]any{"type": "response_item", "timestamp": time.Now().Format(time.RFC3339Nano), "payload": map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call", "arguments": "{\"cmd\":\"go test ./...\"}"}},
		map[string]any{"type": "response_item", "timestamp": time.Now().Format(time.RFC3339Nano), "payload": map[string]any{"type": "function_call_output", "call_id": "call", "output": "Process exited with code 1"}},
		map[string]any{"type": "event_msg", "timestamp": time.Now().Format(time.RFC3339Nano), "payload": map[string]any{"type": "task_complete"}},
	}
	content := []byte{}
	for _, r := range records {
		b, _ := json.Marshal(r)
		content = append(content, b...)
		content = append(content, '\n')
	}
	_ = os.WriteFile(filepath.Join(dir, "rollout-cli.jsonl"), content, 0600)
	got := observe(root, home)
	if got.Connection != "ready" || len(got.Sessions) != 1 || len(got.Events) != 2 || got.SessionCount != 0 {
		t.Fatalf("expected JSONL history without SQLite: %+v", got)
	}
	found := false
	for _, e := range got.Events {
		if e.Kind == "test" && e.Status == "failed" {
			found = true
		}
	}
	if !found {
		t.Fatal(got.Events)
	}
}
func TestSwiftActivityRangesAreConservative(t *testing.T) {
	lines := []string{"struct Shopping: View {", "    var body: some View {", "        Text(\"Hello\")", "    }", "}", "struct Other: View {}"}
	if end := swiftActivityEnd(lines, 1, "struct Shopping:"); end != 5 {
		t.Fatalf("view should exclude following sibling, got %d", end)
	}
	lines[2] = "        Text(\"\"\""
	if end := swiftActivityEnd(lines, 1, "struct Shopping:"); end != 0 {
		t.Fatal("ambiguous multiline literal should remain file-only")
	}
}
