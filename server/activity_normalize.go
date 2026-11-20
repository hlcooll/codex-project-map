package main

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type activityTool struct {
	Status   string            `json:"status"`
	Command  string            `json:"command"`
	Cwd      string            `json:"cwd"`
	Text     string            `json:"text"`
	Content  json.RawMessage   `json:"content"`
	Output   string            `json:"aggregatedOutput"`
	ExitCode *int              `json:"exitCode"`
	Server   string            `json:"server"`
	Tool     string            `json:"tool"`
	Query    string            `json:"query"`
	Error    json.RawMessage   `json:"error"`
	Errors   []json.RawMessage `json:"errors"`
	Changes  []struct {
		Path string `json:"path"`
		Diff string `json:"diff"`
		Kind struct {
			Type string `json:"type"`
		} `json:"kind"`
	} `json:"changes"`
	Actions []struct {
		Type    string `json:"type"`
		Path    string `json:"path"`
		Command string `json:"command"`
	} `json:"commandActions"`
}

var activityHunk = regexp.MustCompile(`(?m)^@@[^\n]*\+(\d+)(?:,(\d+))?[^\n]*@@`)
var activityReadRange = regexp.MustCompile(`(?:sed\s+-n\s+['"]?)(\d+),(\d+)p`)
var activityTestCommand = regexp.MustCompile(`(?:^|[\s;|"'])(?:[^\s;|"']*/)?(?:go(?:\s+-C\s+[^\s]+)?\s+test|(?:pnpm|npm|yarn|bun)\s+(?:run\s+)?test|pytest|vitest)(?:\s|["']|$)`)

func activityText(raw json.RawMessage) string {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var out []string
		for _, p := range parts {
			if p.Text != "" {
				out = append(out, p.Text)
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}
func normalizeActivity(root string, t observedThread, item observedItem) (activity, bool) {
	var tool activityTool
	e := activity{ID: checksum(t.id + ":" + item.id), Kind: "tool", Status: "unknown", At: time.UnixMilli(item.at), Paths: []string{}, Targets: []activityTarget{}, Mapping: "unmapped"}
	if json.Unmarshal(item.raw, &tool) != nil || item.at < 1 {
		return e, false
	}
	switch tool.Status {
	case "inProgress", "running":
		e.Status = "running"
	case "completed", "succeeded", "success":
		e.Status = "completed"
	case "failed", "error", "errored", "declined":
		e.Status = "failed"
	case "interrupted", "cancelled", "canceled":
		e.Status = "interrupted"
	}
	if len(tool.Errors) > 0 || (len(tool.Error) > 0 && string(tool.Error) != "null" && string(tool.Error) != "false" && string(tool.Error) != "\"\"") {
		e.Status = "failed"
	}
	track := func(base, path, text string, line, end int) {
		if path == "" {
			return
		}
		rel, ok := relativePath(root, base, path)
		if !ok || rel == "." {
			return
		}
		for _, existing := range e.Locations {
			if existing.Path == rel && existing.Line == line && existing.Text == text {
				return
			}
		}
		if len(e.Locations) >= 24 {
			return
		}
		e.Locations = append(e.Locations, activityLocation{Path: rel, Line: line, EndLine: end, Text: text})
		found := false
		for _, p := range e.Paths {
			if p == rel {
				found = true
			}
		}
		if !found {
			e.Paths = append(e.Paths, rel)
		}
	}
	switch item.kind {
	case "userMessage", "agentMessage":
		e.Kind = "message"
		e.Status = "completed"
		e.Role = "assistant"
		e.Title = "Codex 回复"
		e.Detail = clipActivity(tool.Text, 4000)
		if item.kind == "userMessage" {
			e.Role = "user"
			e.Title = "用户消息"
			e.Detail = clipActivity(activityText(tool.Content), 4000)
		}
		return e, e.Detail != "" && t.association == "workspace"
	case "fileChange":
		e.Kind = "write"
		e.Title = "修改文件"
		for _, change := range tool.Changes {
			// A hunk's changed text may match a declaration. A whole-file rewrite is
			// deliberately NOT treated as proof that every symbol in it was modified.
			hunks := activityHunk.FindAllStringSubmatchIndex(change.Diff, -1)
			if len(hunks) == 0 {
				track(t.cwd, change.Path, "", 0, 0)
			}
			for j, h := range hunks {
				line, _ := strconv.Atoi(change.Diff[h[2]:h[3]])
				end := len(change.Diff)
				if j+1 < len(hunks) {
					end = hunks[j+1][0]
				}
				cursor := line
				for _, l := range strings.Split(strings.TrimPrefix(change.Diff[h[1]:end], "\n"), "\n") {
					switch {
					case strings.HasPrefix(l, "+"):
						track(t.cwd, change.Path, l[1:], cursor, cursor)
						cursor++
					case strings.HasPrefix(l, "-"):
						track(t.cwd, change.Path, l[1:], 0, 0)
					case strings.HasPrefix(l, " "):
						cursor++
					}
				}
			}
			e.Detail += change.Path + "\n" + clipActivity(change.Diff, 1800) + "\n"
		}
	case "commandExecution":
		e.Kind = "command"
		e.Title = "执行命令"
		e.Detail = clipActivity(tool.Command, 2200)
		if tool.ExitCode != nil {
			if *tool.ExitCode != 0 {
				e.Status = "failed"
			} else if e.Status == "unknown" || e.Status == "completed" {
				e.Status = "completed"
			}
		} else if e.Status == "completed" {
			e.Status = "unknown"
		}
		base := t.cwd
		if tool.Cwd != "" {
			if filepath.IsAbs(tool.Cwd) {
				base = tool.Cwd
			} else {
				base = filepath.Join(base, tool.Cwd)
			}
		}
		read := false
		for _, a := range tool.Actions {
			line, end := 0, 0
			if a.Type == "read" {
				read = true
				if m := activityReadRange.FindStringSubmatch(a.Command); len(m) > 0 {
					line, _ = strconv.Atoi(m[1])
					end, _ = strconv.Atoi(m[2])
				}
			}
			if a.Type == "search" || a.Type == "listFiles" {
				read = true
			}
			// Structured actions are provenance, command strings alone are not paths.
			proof := ""
			if line > 0 && len(tool.Output) <= 200000 {
				proof = tool.Output
			}
			track(base, a.Path, proof, line, end)
		}
		if read {
			e.Kind = "read"
			e.Title = "读取或搜索代码"
		}
		if activityTestCommand.MatchString(tool.Command) {
			e.Kind = "test"
			e.Title = "运行测试"
		}
		if tool.Output != "" {
			e.Detail += "\n\n" + clipActivity(tool.Output, 1400)
		}
		if tool.ExitCode != nil {
			e.Detail += "\n退出码：" + strconv.Itoa(*tool.ExitCode)
		}
		// A cwd inside the selected project is sufficient to display the operation,
		// but not to assign a specific function or card.
		if len(e.Paths) == 0 && t.association != "workspace" && projectCwd(root, base) {
			e.Paths = append(e.Paths, ".")
		}
	case "mcpToolCall":
		e.Title = "工具调用 · " + tool.Server + " / " + tool.Tool
		e.Detail = "" // arbitrary arguments may contain credentials
	case "collabAgentToolCall", "subAgentActivity":
		e.Title = "子代理 · " + tool.Tool
	case "webSearch":
		e.Kind = "read"
		e.Title = "搜索资料"
		e.Detail = clipActivity(tool.Query, 1000)
		if e.Status == "unknown" {
			e.Status = "completed"
		}
	default:
		return e, false
	}
	e.Detail = clipActivity(e.Detail, 4000)
	return e, true
}
