package main

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"
)

type storedProjectActivity struct {
	Event activity `json:"event"`
	Proof []string `json:"proof"`
}

func (p *projectPlanner) live(item project, raw []byte) []byte {
	var result snapshot
	if json.Unmarshal(raw, &result) != nil {
		return raw
	}
	runs, err := p.runs(item.ID)
	if err != nil {
		return raw
	}
	byID := map[string]activity{}
	owned := map[string]bool{}
	for _, e := range result.Events {
		byID[e.ID] = e
	}
	sessions := map[string]liveSession{}
	for _, s := range result.Sessions {
		sessions[s.ID] = s
	}
	seenThreads := map[string]bool{}
	for _, run := range runs {
		if run.ThreadID == "" {
			continue
		}
		rows, err := p.db.Query(`SELECT data FROM project_run_events WHERE run_id=? ORDER BY at DESC LIMIT 80`, run.ID)
		if err != nil {
			continue
		}
		for rows.Next() {
			var text string
			if rows.Scan(&text) != nil {
				continue
			}
			var stored storedProjectActivity
			if json.Unmarshal([]byte(text), &stored) != nil {
				continue
			}
			e := stored.Event
			for i := range e.Locations {
				if i < len(stored.Proof) {
					e.Locations[i].Text = stored.Proof[i]
				}
			}
			if e.Status == "running" && run.Status != "running" && run.Status != "starting" {
				e.Status = "interrupted"
			}
			if run.Role == "plan" && run.Status == "completed" && e.Kind == "message" && e.Role == "assistant" && strings.HasPrefix(strings.TrimSpace(e.Detail), "{") {
				e.Detail = run.Reply
				e.Title = "项目档案已更新"
			}
			// The owned stream is authoritative for this item; native history can lag.
			if _, exists := byID[e.ID]; !exists || !seenThreads[run.ThreadID] {
				byID[e.ID] = e
				owned[e.ID] = true
			}
		}
		rows.Close()
		if !seenThreads[run.ThreadID] {
			status := "idle"
			if run.Status == "running" || run.Status == "starting" {
				status = "working"
			}
			if run.Status == "interrupted" {
				status = "interrupted"
			}
			cwd := item.Root
			if run.Context != nil {
				cwd = run.Context.Cwd
			}
			sessions[checksum(run.ThreadID)] = liveSession{ID: checksum(run.ThreadID), Title: run.Title, Cwd: cwd, LastAt: run.UpdatedAt, Status: status, Association: "workspace"}
			seenThreads[run.ThreadID] = true
		}
	}
	result.Events = []activity{}
	for _, e := range byID {
		result.Events = append(result.Events, e)
	}
	sort.Slice(result.Events, func(i, j int) bool { return result.Events[i].At.After(result.Events[j].At) })
	if len(result.Events) > 180 {
		result.Events = result.Events[:180]
	}
	result.Sessions = []liveSession{}
	result.SessionCount = 0
	for _, s := range sessions {
		result.Sessions = append(result.Sessions, s)
		if s.Status == "working" {
			result.SessionCount++
		}
	}
	sort.Slice(result.Sessions, func(i, j int) bool { return result.Sessions[i].LastAt.After(result.Sessions[j].LastAt) })
	if len(runs) > 0 && result.Connection == "unavailable" {
		result.Connection = "ready"
		result.Message = "当前显示工作台创建的会话记录；本机其他会话记录暂不可用。"
	}
	// Map owned stream events into the existing native App graph too.
	mapped := snapshot{Events: []activity{}}
	for _, event := range result.Events {
		if owned[event.ID] {
			mapped.Events = append(mapped.Events, event)
		}
	}
	if len(mapped.Events) > 0 {
		attachActivityTargets(item.Root, &mapped)
	}
	positions := map[string]int{}
	for i, event := range result.Events {
		positions[event.ID] = i
	}
	for _, event := range mapped.Events {
		result.Events[positions[event.ID]] = event
	}
	if doc, _, _, err := p.document(item); err == nil {
		attachPlanTargets(item.Root, doc, &result)
	}
	for i := range result.Events {
		e := &result.Events[i]
		value, _ := json.Marshal([]any{e.Status, e.Title, e.Detail, e.Paths})
		e.Version = checksum(string(value))
	}
	result.AsOf = time.Now()
	out, err := json.Marshal(result)
	if err != nil {
		return raw
	}
	return out
}
func attachPlanTargets(rootPath string, doc planDocument, result *snapshot) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return
	}
	defer root.Close()
	files := map[string][]string{}
	for i := range result.Events {
		event := &result.Events[i]
		if event.Kind == "message" {
			continue
		}
		for _, node := range doc.Nodes {
			seen := map[string]bool{}
			for _, proof := range node.Evidence {
				if !proof.Valid || seen[proof.File] {
					continue
				}
				for _, loc := range event.Locations {
					if loc.Path != proof.File {
						continue
					}
					seen[proof.File] = true
					precision := "file"
					if proof.Match != "" && proof.Line > 0 && time.Since(event.At) < 10*time.Minute {
						lines, ok := files[proof.File]
						if !ok {
							lines = strings.Split(string(planFile(root, proof.File)), "\n")
							files[proof.File] = lines
						}
						end := 0
						for _, span := range activityDeclarations(proof.File, lines) {
							if span.start == proof.Line {
								end = span.end
							}
						}
						if strings.HasSuffix(proof.File, ".swift") {
							end = swiftActivityEnd(lines, proof.Line, proof.Match)
						}
						if loc.Line >= proof.Line && loc.EndLine <= end && end >= proof.Line && verifiedActivityLines(event.Kind, loc, lines) {
							precision = "symbol"
						}
					}
					event.Targets = append(event.Targets, activityTarget{Group: "项目图谱", Screen: node.Title, Key: node.ID, File: proof.File, Precision: precision})
					if precision == "symbol" {
						event.Mapping = "exact"
					} else if event.Mapping == "unmapped" {
						event.Mapping = "file"
					}
					break
				}
			}
		}
	}
}
