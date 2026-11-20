package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type activityLocation struct {
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	EndLine int    `json:"endLine,omitempty"`
	Text    string `json:"-"`
}
type activityTarget struct {
	Group     string `json:"group"`
	Screen    string `json:"screen"`
	Key       string `json:"key,omitempty"`
	File      string `json:"file"`
	Precision string `json:"precision"`
}
type activity struct {
	ID        string             `json:"id"`
	Version   string             `json:"version"`
	ThreadID  string             `json:"threadId"`
	TurnID    string             `json:"turnId"`
	Kind      string             `json:"kind"`
	Status    string             `json:"status"`
	Role      string             `json:"role,omitempty"`
	At        time.Time          `json:"at"`
	Title     string             `json:"title"`
	Detail    string             `json:"detail"`
	Paths     []string           `json:"paths"`
	Locations []activityLocation `json:"locations,omitempty"`
	Targets   []activityTarget   `json:"targets"`
	Mapping   string             `json:"mapping"`
}
type liveSession struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Cwd         string    `json:"cwd"`
	LastAt      time.Time `json:"lastAt"`
	Status      string    `json:"status"`
	Association string    `json:"association"`
}
type snapshot struct {
	Project struct {
		Name string `json:"name"`
		Root string `json:"root"`
	} `json:"project"`
	Source       string        `json:"source"`
	Connection   string        `json:"connection"`
	AsOf         time.Time     `json:"asOf"`
	SessionCount int           `json:"sessionCount"`
	Sessions     []liveSession `json:"sessions"`
	Events       []activity    `json:"events"`
	Message      string        `json:"message,omitempty"`
}
type observedThread struct {
	id, cwd, title, rollout, turn, state string
	updated                              int64
	items                                []observedItem
	association                          string
	limited                              bool
}
type observedItem struct {
	id, turn, kind string
	at, ordinal    int64
	raw            json.RawMessage
}

func observe(root, home string) snapshot {
	now := time.Now()
	result := snapshot{Source: "live", Connection: "ready", AsOf: now, Sessions: []liveSession{}, Events: []activity{}}
	result.Project.Name = filepath.Base(root)
	result.Project.Root = root
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	threads, sqliteOK, issues := readActivitySQLite(ctx, root, home)
	// SQLite supplies current desktop turns. JSONL supplies CLI/missing histories.
	fallback, jsonOK, jsonIssues := readActivityJSONL(ctx, root, home, threads)
	issues = append(issues, jsonIssues...)
	threads = append(threads, fallback...)
	if !sqliteOK && !jsonOK {
		result.Connection = "unavailable"
		result.Message = "暂时无法读取本机 Codex 记录。"
		return result
	}
	if len(issues) > 0 {
		result.Connection = "degraded"
		unique := []string{}
		seen := map[string]bool{}
		for _, issue := range issues {
			if !seen[issue] {
				unique = append(unique, issue)
				seen[issue] = true
			}
		}
		result.Message = strings.Join(unique, "；")
	}
	for _, thread := range threads {
		if thread.limited && result.Message == "" {
			result.Message = "部分会话日志较长，仅显示末尾近期记录。"
		}
		own := thread.association == "workspace"
		sid := checksum(thread.id)
		latest := time.Unix(thread.updated, 0)
		if !own {
			latest = time.Time{}
		}
		for _, item := range thread.items {
			if !own {
				e, ok := normalizeActivity(root, thread, item)
				if !ok || len(e.Paths) == 0 {
					continue
				}
			}
			if at := time.UnixMilli(item.at); at.After(latest) {
				latest = at
			}
		}
		events := []activity{}
		for _, item := range thread.items {
			event, keep := normalizeActivity(root, thread, item)
			if !keep {
				continue
			}
			if !own && len(event.Paths) == 0 {
				continue
			}
			event.ThreadID = sid
			event.TurnID = checksum(thread.id + ":" + item.turn)
			if event.Status == "running" {
				switch {
				case thread.state == "unknown":
					event.Status = "stale"
				case item.turn != thread.turn || thread.state != "inProgress":
					event.Status = "interrupted"
				case now.Sub(latest) > 5*time.Minute:
					event.Status = "stale"
				}
			}
			events = append(events, event)
		}
		if !own && len(events) == 0 {
			continue
		}
		status := "idle"
		if thread.state == "unknown" {
			status = "stale"
		}
		if thread.state == "inProgress" {
			if now.Sub(latest) <= 5*time.Minute {
				status = "working"
			} else {
				status = "stale"
			}
		}
		if thread.state == "interrupted" {
			status = "interrupted"
		}
		// A foreign conversation may still be active on another project. Only an
		// in-progress operation with direct path evidence makes THIS project busy.
		if !own {
			status = "idle"
			for _, e := range events {
				if e.Status == "running" {
					status = "working"
				}
				if e.Status == "stale" && status != "working" {
					status = "stale"
				}
			}
		}
		if status == "working" {
			result.SessionCount++
		}
		result.Sessions = append(result.Sessions, liveSession{ID: sid, Title: clipActivity(thread.title, 100), Cwd: thread.cwd, LastAt: latest, Status: status, Association: thread.association})
		result.Events = append(result.Events, events...)
	}
	sort.Slice(result.Sessions, func(i, j int) bool {
		if (result.Sessions[i].Status == "working") != (result.Sessions[j].Status == "working") {
			return result.Sessions[i].Status == "working"
		}
		return result.Sessions[i].LastAt.After(result.Sessions[j].LastAt)
	})
	sort.Slice(result.Events, func(i, j int) bool {
		if result.Events[i].At.Equal(result.Events[j].At) {
			return result.Events[i].ID < result.Events[j].ID
		}
		return result.Events[i].At.After(result.Events[j].At)
	})
	if len(result.Events) > 160 {
		// Keep context for every listed conversation before filling recent history.
		kept, counts := map[string]bool{}, map[string]int{}
		selected := []activity{}
		for _, e := range result.Events {
			if counts[e.ThreadID] < 4 || e.Status == "running" {
				selected = append(selected, e)
				kept[e.ID] = true
				counts[e.ThreadID]++
			}
		}
		for _, e := range result.Events {
			if len(selected) >= 160 {
				break
			}
			if !kept[e.ID] {
				selected = append(selected, e)
			}
		}
		sort.Slice(selected, func(i, j int) bool { return selected[i].At.After(selected[j].At) })
		result.Events = selected
	}
	attachActivityTargets(root, &result)
	for i := range result.Events {
		e := &result.Events[i]
		raw, _ := json.Marshal(struct {
			Status, Title, Detail string
			Paths                 []string
		}{e.Status, e.Title, e.Detail, e.Paths})
		e.Version = checksum(string(raw))
	}
	if len(result.Sessions) == 0 && result.Message == "" {
		result.Message = "尚未找到与此项目有关的 Codex 会话。"
	}
	return result
}
func clipActivity(value string, limit int) string {
	value = strings.TrimSpace(value)
	r := []rune(value)
	if len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return value
}
func projectCwd(root, cwd string) bool {
	_, ok := relativePath(root, root, cwd)
	return filepath.IsAbs(cwd) && ok
}
func canonicalCwd(cwd string) string {
	if p, err := filepath.EvalSymlinks(cwd); err == nil {
		return p
	}
	return filepath.Clean(cwd)
}
