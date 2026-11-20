package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
)

func readActivitySQLite(ctx context.Context, root, home string) ([]observedThread, bool, []string) {
	state, err := readonlyDB(ctx, filepath.Join(home, "state_5.sqlite"))
	if err != nil {
		return nil, false, nil
	}
	defer state.Close()
	history, err := readonlyDB(ctx, filepath.Join(home, "thread_history_1.sqlite"))
	if err != nil {
		return nil, false, []string{"会话历史暂不可用，尝试日志记录"}
	}
	defer history.Close()
	// Current workspace history is retained even when older than 24h. Recent
	// foreign conversations are candidates only; their events need exact path proof.
	prefix := strings.TrimRight(root, string(filepath.Separator)) + string(filepath.Separator)
	candidates := []observedThread{}
	issues := []string{}
	for _, own := range []bool{true, false} {
		clause := "(cwd=? OR substr(cwd,1,length(?))=?)"
		params := []any{root, prefix, prefix}
		limit := 12
		if !own {
			clause = "NOT " + clause + " AND updated_at>=?"
			params = append(params, time.Now().Add(-24*time.Hour).Unix())
			limit = 24
		}
		params = append(params, limit)
		rows, e := state.QueryContext(ctx, `SELECT id,cwd,COALESCE(name,title,''),COALESCE(rollout_path,''),updated_at FROM threads WHERE archived=0 AND `+clause+` ORDER BY updated_at DESC LIMIT ?`, params...)
		if e != nil {
			return nil, false, []string{"会话索引格式暂不兼容"}
		}
		for rows.Next() {
			var t observedThread
			if rows.Scan(&t.id, &t.cwd, &t.title, &t.rollout, &t.updated) != nil {
				issues = append(issues, "部分会话索引无法解析")
				continue
			}
			t.cwd = canonicalCwd(t.cwd)
			t.association = "cross-project"
			if projectCwd(root, t.cwd) {
				t.association = "workspace"
			}
			candidates = append(candidates, t)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, false, []string{"会话索引读取中断"}
		}
	}
	threads := []observedThread{}
	for _, t := range candidates {
		if ctx.Err() != nil {
			issues = append(issues, "部分会话等待下一次采集")
			break
		}
		err = history.QueryRowContext(ctx, `SELECT turn_id,status FROM thread_turns WHERE thread_id=? ORDER BY rollout_ordinal DESC LIMIT 1`, t.id).Scan(&t.turn, &t.state)
		if err == sql.ErrNoRows {
			t.state = "unknown"
		} else if err != nil {
			issues = append(issues, "部分会话状态读取失败")
			continue
		}
		items, e := history.QueryContext(ctx, `SELECT item_id,COALESCE(turn_id,''),item_type,COALESCE(completed_at_ms,started_at_ms,created_at_ms),rollout_ordinal,item_json FROM thread_items WHERE thread_id=? AND item_type IN ('userMessage','agentMessage','commandExecution','fileChange','mcpToolCall','collabAgentToolCall','subAgentActivity','webSearch') ORDER BY rollout_ordinal DESC LIMIT 90`, t.id)
		if e != nil {
			issues = append(issues, "部分会话事件读取失败")
			continue
		}
		for items.Next() {
			var item observedItem
			var raw []byte
			if items.Scan(&item.id, &item.turn, &item.kind, &item.at, &item.ordinal, &raw) != nil {
				issues = append(issues, "部分事件字段无法解析")
				continue
			}
			if len(raw) > 1600000 {
				issues = append(issues, "部分事件过大，已跳过")
				continue
			}
			if !json.Valid(raw) {
				issues = append(issues, "部分事件内容无法解析")
				continue
			}
			item.raw = raw
			t.items = append(t.items, item)
		}
		e = items.Err()
		items.Close()
		if e != nil {
			issues = append(issues, "部分会话事件读取中断")
		}
		// No persisted items: permit the JSONL reader to recover this conversation.
		if len(t.items) > 0 {
			threads = append(threads, t)
		}
	}
	return threads, true, issues
}
