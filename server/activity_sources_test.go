package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActivitySQLiteBadSessionAndRowsDoNotHideHealthyHistory(t *testing.T) {
	root, home, state, history := activityFixture(t)
	now := time.Now()
	_, _ = state.Exec(`INSERT INTO threads VALUES ('bad',?,'bad','','',?,0),('good',?,'good','','',?,0)`, root, now.Unix()+1, root, now.Unix())
	_, _ = history.Exec(`INSERT INTO thread_turns VALUES ('bad','turn',NULL,1),('good','turn','completed',1)`)
	fixtureItem(t, history, "good", "turn", "ok", "agentMessage", now, map[string]any{"text": "healthy"})
	fixtureItem(t, history, "good", "turn", "large", "agentMessage", now, map[string]any{"text": strings.Repeat("x", 1600001)})
	_, _ = history.Exec(`INSERT INTO thread_items VALUES ('good','turn','malformed','agentMessage','not-time',NULL,NULL,1,'{}')`)
	got := observe(root, home)
	if got.Connection != "degraded" || len(got.Events) != 1 || got.Events[0].Detail != "healthy" {
		t.Fatalf("bad source should degrade without losing healthy events: %+v", got)
	}
	for _, word := range []string{"状态读取失败", "事件过大", "字段无法解析"} {
		if !strings.Contains(got.Message, word) {
			t.Fatalf("missing diagnostic %s: %s", word, got.Message)
		}
	}
}
func TestActivityJSONLReportsDamagedAndOversizeRecords(t *testing.T) {
	root := canonicalCwd(t.TempDir())
	for _, extra := range []string{"{broken}\n", strings.Repeat("x", (2<<20)+1) + "\n"} {
		home := t.TempDir()
		dir := filepath.Join(home, "sessions")
		_ = os.MkdirAll(dir, 0700)
		metadata, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{"id": "cli", "cwd": root}})
		message, _ := json.Marshal(map[string]any{"type": "response_item", "timestamp": time.Now().Format(time.RFC3339Nano), "payload": map[string]any{"type": "message", "role": "user", "content": "hello"}})
		_ = os.WriteFile(filepath.Join(dir, "rollout.jsonl"), []byte(string(metadata)+"\n"+string(message)+"\n"+extra), 0600)
		got := observe(root, home)
		if got.Connection != "degraded" || len(got.Events) != 1 {
			t.Fatalf("corrupted tail must not look healthy: %+v", got)
		}
	}
}
func TestActivityExitCodeIsSignedAndBounded(t *testing.T) {
	for _, tc := range []struct {
		input string
		value int
		found bool
	}{
		{"Process exited with code -1", -1, true}, {"Process exited with code +1", 1, true}, {`{"exit_code":0}`, 0, true},
		{"Process exited with code 99999999999999999999", 0, false}, {"Process exited with code unknown 0", 0, false}, {"Process exited with code 1oops", 0, false},
	} {
		value, found := activityOutputExit(tc.input)
		if found != tc.found || (found && value != tc.value) {
			t.Fatalf("%q returned %d,%v", tc.input, value, found)
		}
	}
}

func TestActivityLargeImageRecordDoesNotHideTurnCompletion(t *testing.T) {
	root := canonicalCwd(t.TempDir())
	home := t.TempDir()
	dir := filepath.Join(home, "sessions")
	_ = os.MkdirAll(dir, 0700)
	at := time.Now().Format(time.RFC3339Nano)
	records := []any{
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": "image", "cwd": root}},
		map[string]any{"type": "event_msg", "timestamp": at, "payload": map[string]any{"type": "task_started", "turn_id": "turn"}},
		map[string]any{"type": "response_item", "timestamp": at, "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_image", "image_url": strings.Repeat("x", (2<<20)+1)}, map[string]any{"type": "input_text", "text": "inspect image"}}}},
		map[string]any{"type": "event_msg", "timestamp": at, "payload": map[string]any{"type": "task_complete"}},
	}
	var content []byte
	for _, r := range records {
		b, _ := json.Marshal(r)
		content = append(content, b...)
		content = append(content, '\n')
	}
	_ = os.WriteFile(filepath.Join(dir, "image.jsonl"), content, 0600)
	got := observe(root, home)
	if got.Connection != "ready" || len(got.Sessions) != 1 || got.Sessions[0].Status != "idle" || got.SessionCount != 0 || len(got.Events) != 1 {
		t.Fatalf("image payload must not hide the end of a turn: %+v", got)
	}
}
