package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type logCandidate struct {
	path    string
	info    fs.FileInfo
	id, cwd string
}

var activityLogs = struct {
	sync.Mutex
	home      string
	at        time.Time
	files     []logCandidate
	available bool
	issues    []string
}{}

func activityLogFiles(ctx context.Context, home string) ([]logCandidate, bool, []string) {
	activityLogs.Lock()
	defer activityLogs.Unlock()
	if activityLogs.home == home && time.Since(activityLogs.at) < 15*time.Second {
		return activityLogs.files, activityLogs.available, activityLogs.issues
	}
	candidates := []logCandidate{}
	available := false
	issues := []string{}
	for _, base := range []string{"sessions", "archived_sessions"} {
		dir := filepath.Join(home, base)
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		available = true
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return fs.SkipAll
			}
			if err != nil {
				return nil
			}
			if !d.IsDir() && d.Type().IsRegular() && strings.HasSuffix(d.Name(), ".jsonl") {
				if info, e := d.Info(); e == nil {
					candidates = append(candidates, logCandidate{path: p, info: info})
				}
			}
			return nil
		})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].info.ModTime().After(candidates[j].info.ModTime()) })
	if len(candidates) > 1000 {
		candidates = candidates[:1000]
		issues = append(issues, "日志候选仅保留最近 1000 个文件")
	}
	files := []logCandidate{}
	for _, c := range candidates {
		if ctx.Err() != nil {
			issues = append(issues, "部分日志等待下一次采集")
			break
		}
		f, err := os.Open(c.path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(io.LimitReader(f, 128<<10))
		scanner.Buffer(make([]byte, 4096), 128<<10)
		for n := 0; n < 3 && scanner.Scan(); n++ {
			var r struct {
				Type    string `json:"type"`
				Payload struct {
					ID        string `json:"id"`
					SessionID string `json:"session_id"`
					Cwd       string `json:"cwd"`
				} `json:"payload"`
			}
			if json.Unmarshal(scanner.Bytes(), &r) == nil && r.Type == "session_meta" {
				c.id = r.Payload.ID
				if c.id == "" {
					c.id = r.Payload.SessionID
				}
				c.cwd = canonicalCwd(r.Payload.Cwd)
				break
			}
		}
		f.Close()
		if c.id != "" && filepath.IsAbs(c.cwd) {
			files = append(files, c)
		}
	}
	activityLogs.home = home
	activityLogs.at = time.Now()
	activityLogs.files = files
	activityLogs.available = available
	activityLogs.issues = issues
	return files, available, issues
}
func readActivityJSONL(ctx context.Context, root, home string, known []observedThread) ([]observedThread, bool, []string) {
	files, available, issues := activityLogFiles(ctx, home)
	seen := map[string]bool{}
	for _, t := range known {
		seen[t.id] = true
	}
	result := []observedThread{}
	for _, c := range files {
		if ctx.Err() != nil {
			break
		}
		if seen[c.id] || !projectCwd(root, c.cwd) {
			continue
		}
		if len(result) >= 6 {
			break
		}
		t, ok, parseIssues := parseActivityLog(c)
		issues = append(issues, parseIssues...)
		if ok {
			result = append(result, t)
			seen[c.id] = true
		} else if !t.limited || len(parseIssues) > 0 {
			issues = append(issues, "部分会话日志暂无法解析")
		}
	}
	return result, available, issues
}
func parseActivityLog(c logCandidate) (observedThread, bool, []string) {
	issues := []string{}
	t := observedThread{id: c.id, cwd: c.cwd, title: "Codex 会话", updated: c.info.ModTime().Unix(), association: "workspace", state: "unknown"}
	f, err := os.Open(c.path)
	if err != nil {
		return t, false, []string{"部分会话日志无法打开"}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return t, false, []string{"部分会话日志信息不可用"}
	}
	t.updated = info.ModTime().Unix()
	// Read a bounded tail. Partial first/last lines are never treated as events.
	offset := max(int64(0), info.Size()-(4<<20))
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return t, false, []string{"部分会话日志读取失败"}
	}
	t.limited = offset > 0
	reader := bufio.NewReaderSize(f, 64<<10)
	if offset > 0 {
		_, _ = reader.ReadString('\n')
	}
	scanner := bufio.NewScanner(reader)
	// Image-bearing user messages can exceed 2 MiB. The entire input is already
	// bounded to a 4 MiB tail, so accept a complete record within that window.
	scanner.Buffer(make([]byte, 64<<10), (4<<20)+16)
	calls := map[string]int{}
	count := int64(0)
	for scanner.Scan() {
		count++
		var record struct {
			Type      string                     `json:"type"`
			Timestamp string                     `json:"timestamp"`
			Payload   map[string]json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(scanner.Bytes(), &record) != nil {
			issues = append(issues, "部分日志行不完整或已损坏")
			continue
		}
		if record.Type != "event_msg" && record.Type != "response_item" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, record.Timestamp)
		if err != nil {
			issues = append(issues, "部分日志时间无法解析")
			continue
		}
		str := func(key string) string { var s string; _ = json.Unmarshal(record.Payload[key], &s); return s }
		kind := str("type")
		if record.Type == "event_msg" {
			switch kind {
			case "task_started":
				t.turn = str("turn_id")
				t.state = "inProgress"
			case "task_complete", "task_completed":
				t.state = "completed"
			case "turn_aborted":
				t.state = "interrupted"
			}
			continue
		}
		if record.Type != "response_item" {
			continue
		}
		item := observedItem{id: checksum(record.Timestamp + string(scanner.Bytes())), turn: t.turn, at: at.UnixMilli(), ordinal: count}
		data := map[string]any{}
		switch kind {
		case "message":
			role := str("role")
			if role != "user" && role != "assistant" {
				continue
			}
			text := activityText(record.Payload["content"])
			if text == "" {
				continue
			}
			if role == "user" {
				item.kind = "userMessage"
				data["content"] = text
				if t.title == "Codex 会话" {
					t.title = clipActivity(text, 70)
				}
			} else {
				item.kind = "agentMessage"
				data["text"] = text
			}
		case "function_call", "custom_tool_call":
			name := str("name")
			id := str("call_id")
			if id != "" {
				item.id = id
			}
			data["status"] = "inProgress"
			var args map[string]any
			raw := str("arguments")
			if raw == "" {
				raw = str("input")
			}
			_ = json.Unmarshal([]byte(raw), &args)
			if strings.HasSuffix(name, "exec_command") || strings.HasSuffix(name, "shell_command") {
				item.kind = "commandExecution"
				data["command"] = args["cmd"]
				if data["command"] == nil {
					data["command"] = args["command"]
				}
				data["cwd"] = args["workdir"]
			} else if strings.HasSuffix(name, "apply_patch") {
				item.kind = "fileChange"
				data["changes"] = activityPatchChanges(raw)
			} else {
				item.kind = "mcpToolCall"
				data["server"] = "Codex"
				data["tool"] = name
			}
			calls[id] = len(t.items)
		case "function_call_output", "custom_tool_call_output":
			id := str("call_id")
			index, exists := calls[id]
			if !exists {
				continue
			}
			_ = json.Unmarshal(t.items[index].raw, &data)
			output := str("output")
			reportedError := false
			var wrapper struct {
				IsError bool            `json:"isError"`
				Error   json.RawMessage `json:"error"`
			}
			resultRaw := record.Payload["output"]
			if output != "" {
				resultRaw = []byte(output)
			}
			if json.Unmarshal(resultRaw, &wrapper) == nil {
				reportedError = wrapper.IsError || (len(wrapper.Error) > 0 && string(wrapper.Error) != "null")
			}
			data["status"] = "completed"
			if strings.Contains(output, "session ID") || strings.Contains(output, "session_id") || strings.Contains(output, "Script running with cell ID") {
				data["status"] = "inProgress"
			}
			if code, found := activityOutputExit(output); found {
				data["exitCode"] = code
				if code != 0 {
					data["status"] = "failed"
				}
			}
			if reportedError || strings.Contains(output, "Failed to find expected lines") || strings.Contains(output, "patch rejected") {
				data["status"] = "failed"
			}
			data["aggregatedOutput"] = clipActivity(output, 1400)
			t.items[index].raw, _ = json.Marshal(data)
			t.items[index].at = at.UnixMilli()
			continue
		default:
			continue
		}
		item.raw, _ = json.Marshal(data)
		t.items = append(t.items, item)
	}
	if scanner.Err() != nil {
		issues = append(issues, "部分会话日志读取中断或单行过长")
	}
	if len(issues) > 0 {
		t.state = "unknown"
	}
	if len(t.items) > 90 {
		t.items = t.items[len(t.items)-90:]
	}
	return t, len(t.items) > 0, issues
}

var activityExitPattern = regexp.MustCompile(`(?:Process exited with code\s+|"exit_code"\s*:\s*)([+-]?\d+)(?:\s|[,}]|$)`)

func activityOutputExit(output string) (int, bool) {
	if match := activityExitPattern.FindStringSubmatch(output); len(match) > 0 {
		n, err := strconv.ParseInt(match[1], 10, 32)
		return int(n), err == nil
	}
	return 0, false
}
func activityPatchChanges(input string) []map[string]any {
	out := []map[string]any{}
	for _, line := range strings.Split(input, "\n") {
		for _, prefix := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: "} {
			if strings.HasPrefix(line, prefix) {
				out = append(out, map[string]any{"path": strings.TrimSpace(strings.TrimPrefix(line, prefix)), "diff": ""})
			}
		}
	}
	return out
}
