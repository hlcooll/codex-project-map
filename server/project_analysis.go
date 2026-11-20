package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type analysisRecord struct {
	SkillKey   string            `json:"skillKey,omitempty"`
	Gaps       []analysisGap     `json:"gaps,omitempty"`
	Inventory  projectInventory  `json:"inventory"`
	Reviewed   map[string]string `json:"reviewed"`
	Reasons    map[string]string `json:"reasons"`
	Integrated string            `json:"integrated"`
}
type analysisModule struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Path     string `json:"path"`
	Total    int    `json:"total"`
	Reviewed int    `json:"reviewed"`
	Status   string `json:"status"`
}
type analysisFile struct {
	Path     string `json:"path"`
	ModuleID string `json:"moduleId"`
	Status   string `json:"status"`
	Reason   string `json:"reason,omitempty"`
}
type analysisView struct {
	Gaps        []analysisGap        `json:"gaps"`
	ScannedAt   time.Time            `json:"scannedAt"`
	Fingerprint string               `json:"fingerprint"`
	Total       int                  `json:"total"`
	Reviewed    int                  `json:"reviewed"`
	Status      string               `json:"status"`
	Integrated  bool                 `json:"integrated"`
	Modules     []analysisModule     `json:"modules"`
	Files       []analysisFile       `json:"files"`
	Excluded    []inventoryExclusion `json:"excluded"`
	Warnings    []string             `json:"warnings"`
	Truncated   bool                 `json:"truncated"`
}

func (a *analysisRecord) view() *analysisView {
	if a == nil {
		return nil
	}
	v := &analysisView{Gaps: append([]analysisGap{}, a.Gaps...), ScannedAt: a.Inventory.ScannedAt, Fingerprint: a.Inventory.Fingerprint, Total: len(a.Inventory.Files), Status: "pending", Integrated: !a.Inventory.Truncated && a.Integrated == a.Inventory.Fingerprint && a.Integrated != "", Modules: []analysisModule{}, Files: []analysisFile{}, Excluded: a.Inventory.Excluded, Warnings: a.Inventory.Warnings, Truncated: a.Inventory.Truncated}
	counts := map[string]int{}
	for _, f := range a.Inventory.Files {
		state := "pending"
		if a.Reviewed[f.Path] == f.Digest {
			state = "reviewed"
			v.Reviewed++
			counts[f.ModuleID]++
		} else if a.Reviewed[f.Path] != "" {
			state = "stale"
		}
		v.Files = append(v.Files, analysisFile{Path: f.Path, ModuleID: f.ModuleID, Status: state, Reason: a.Reasons[f.Path]})
	}
	for _, m := range a.Inventory.Modules {
		status := "pending"
		if counts[m.ID] > 0 {
			status = "partial"
		}
		if counts[m.ID] == m.Files {
			status = "complete"
		}
		v.Modules = append(v.Modules, analysisModule{ID: m.ID, Title: m.Title, Path: m.Path, Total: m.Files, Reviewed: counts[m.ID], Status: status})
	}
	if v.Reviewed > 0 {
		v.Status = "partial"
	}
	if v.Total > 0 && v.Reviewed == v.Total && v.Integrated && !v.Truncated {
		v.Status = "complete"
	}
	return v
}
func (p *projectPlanner) analysis(projectID string) (*analysisRecord, error) {
	var raw string
	err := p.db.QueryRow(`SELECT data FROM project_analysis WHERE project_id=?`, projectID).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var a analysisRecord
	if err = json.Unmarshal([]byte(raw), &a); err != nil {
		return nil, err
	}
	if a.Reviewed == nil {
		a.Reviewed = map[string]string{}
	}
	if a.Reasons == nil {
		a.Reasons = map[string]string{}
	}
	return &a, nil
}
func (p *projectPlanner) saveAnalysis(ctx context.Context, projectID string, a *analysisRecord) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = p.db.ExecContext(ctx, `INSERT INTO project_analysis VALUES (?,?) ON CONFLICT(project_id) DO UPDATE SET data=excluded.data`, projectID, string(raw))
	return err
}
func (p *projectPlanner) refreshAnalysis(ctx context.Context, item project) (*analysisRecord, error) {
	inventory, err := scanProjectInventory(ctx, item.Root)
	if err != nil {
		return nil, err
	}
	a, err := p.analysis(item.ID)
	if err != nil {
		return nil, err
	}
	if a == nil {
		a = &analysisRecord{Reviewed: map[string]string{}, Reasons: map[string]string{}}
	}
	a.Inventory = inventory
	if inventory.Truncated {
		a.Integrated = ""
	}
	if err = p.saveAnalysis(ctx, item.ID, a); err != nil {
		return nil, err
	}
	return a, nil
}
func (p *projectPlanner) scan(w http.ResponseWriter, r *http.Request) {
	item, err := p.projects.project(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p.mu.Lock()
	if _, busy := p.active[item.ID]; busy || p.closing {
		p.mu.Unlock()
		http.Error(w, "项目正在执行任务，请结束后重新扫描", 409)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	p.active[item.ID] = activeProjectRun{id: "inventory", cancel: cancel}
	p.workers.Add(1)
	p.mu.Unlock()
	defer p.workers.Done()
	defer p.releaseRun(item.ID, "inventory")
	defer cancel()
	a, err := p.refreshAnalysis(ctx, item)
	if err != nil {
		http.Error(w, "目录扫描失败："+clipActivity(err.Error(), 200), 500)
		return
	}
	writeJSON(w, a.view())
}

// Keep the entire graph, including earlier module results and explicit App mappings.
func mergeProjectDocuments(base, delta planDocument, origin string) (planDocument, error) {
	nodes := map[string]int{}
	for i, n := range base.Nodes {
		nodes[n.ID] = i
	}
	for _, n := range delta.Nodes {
		if _, duplicate := nodes[n.ID]; !duplicate && len(base.Nodes) >= 4000 {
			return base, errors.New("项目图谱超过4000个节点，请拆分项目后继续")
		}
		n.Preview = nil
		if origin != "" {
			n.Origin = origin
		}
		if i, ok := nodes[n.ID]; ok {
			previous := base.Nodes[i]
			if previous.Origin == "manifest" && origin == "codex" {
				continue
			}
			if n.ModuleID == "" {
				n.ModuleID = previous.ModuleID
			}
			if n.Category == "" {
				n.Category = previous.Category
			}
			if n.Description == "" {
				n.Description = previous.Description
			}
			if len(n.Evidence) == 0 && n.State == "unknown" {
				n.State = previous.State
			}
			n.Evidence = mergePlanEvidence(n.Evidence, previous.Evidence, 10)
			for _, criterion := range previous.Acceptance {
				if !containsString(n.Acceptance, criterion) && len(n.Acceptance) < 12 {
					n.Acceptance = append(n.Acceptance, criterion)
				}
			}
			n.Preview = previous.Preview
			base.Nodes[i] = n
		} else {
			nodes[n.ID] = len(base.Nodes)
			base.Nodes = append(base.Nodes, n)
		}
	}
	edges := map[string]int{}
	for i, e := range base.Edges {
		edges[e.From+"\x00"+e.To+"\x00"+e.Label] = i
	}
	for _, e := range delta.Edges {
		if _, ok := nodes[e.From]; !ok {
			return base, fmt.Errorf("关系起点不存在：%s", e.From)
		}
		if _, ok := nodes[e.To]; !ok {
			return base, fmt.Errorf("关系终点不存在：%s", e.To)
		}
		if origin != "" {
			e.Origin = origin
		}
		key := e.From + "\x00" + e.To + "\x00" + e.Label
		if i, ok := edges[key]; ok {
			if base.Edges[i].Origin != "manifest" {
				e.Evidence = mergePlanEvidence(e.Evidence, base.Edges[i].Evidence, 6)
				e.Scopes = mergePlanScopes(e.Scopes, base.Edges[i].Scopes)
				base.Edges[i] = e
			}
		} else {
			edges[key] = len(base.Edges)
			base.Edges = append(base.Edges, e)
		}
	}
	if delta.Summary != "" {
		base.Summary = delta.Summary
	}
	if base.Kind == "" {
		base.Kind = delta.Kind
	}
	for _, stack := range delta.Stack {
		if !containsString(base.Stack, stack) && len(base.Stack) < 16 {
			base.Stack = append(base.Stack, stack)
		}
	}
	for _, q := range delta.Questions {
		if !containsString(base.Questions, q) && len(base.Questions) < 512 {
			base.Questions = append(base.Questions, q)
		}
	}
	return base, nil
}
func mergePlanEvidence(current, previous []planEvidence, limit int) []planEvidence {
	out := append([]planEvidence{}, current...)
	seen := map[string]bool{}
	for _, e := range current {
		seen[e.File+"\x00"+e.Match] = true
	}
	for _, e := range previous {
		key := e.File + "\x00" + e.Match
		if !seen[key] && len(out) < limit {
			out = append(out, e)
			seen[key] = true
		}
	}
	return out
}
func containsString(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}
func mergePlanScopes(a, b []string) []string {
	out := append([]string{}, a...)
	for _, value := range b {
		if !containsString(out, value) {
			out = append(out, value)
		}
	}
	return out
}

func overlayInventory(item project, doc planDocument, a *analysisRecord) planDocument {
	if a == nil {
		return doc
	}
	seed := seedProjectGraph(item, a.Inventory)
	// Config removals must not leave the previous explicit relationship alive.
	kept := doc.Nodes[:0]
	removed := map[string]bool{}
	for _, n := range doc.Nodes {
		if n.Origin == "manifest" || n.Origin == "scan" {
			removed[n.ID] = true
		} else {
			kept = append(kept, n)
		}
	}
	doc.Nodes = kept
	edges := doc.Edges[:0]
	for _, e := range doc.Edges {
		if e.Origin != "manifest" && e.Origin != "scan" {
			edges = append(edges, e)
		}
	}
	doc.Edges = edges
	previews := map[string]*planPreview{}
	for _, n := range seed.Nodes {
		previews[n.ID] = n.Preview
		delete(removed, n.ID)
	}
	edges = doc.Edges[:0]
	for _, e := range doc.Edges {
		if !removed[e.From] && !removed[e.To] {
			edges = append(edges, e)
		}
	}
	doc.Edges = edges
	summary := doc.Summary
	merged, err := mergeProjectDocuments(doc, seed, "")
	if err != nil {
		return doc
	}
	if summary != "" {
		merged.Summary = summary
	}
	for i := range merged.Nodes {
		if preview := previews[merged.Nodes[i].ID]; preview != nil {
			merged.Nodes[i].Preview = preview
		}
	}
	return normalizeSeed(merged, a.Inventory)
}

type analysisReply struct {
	CapabilityGaps []analysisGap `json:"capabilityGaps"`
	planDocument
	ReviewedFiles []string `json:"reviewedFiles"`
	DeferredFiles []struct {
		File   string `json:"file"`
		Reason string `json:"reason"`
	} `json:"deferredFiles"`
}

func analysisSchema() any {
	schema := planOutputSchema().(map[string]any)
	props := schema["properties"].(map[string]any)
	props["reviewedFiles"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	props["deferredFiles"] = map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"file": map[string]any{"type": "string"}, "reason": map[string]any{"type": "string"}}, "required": []string{"file", "reason"}, "additionalProperties": false}}
	node := props["nodes"].(map[string]any)["items"].(map[string]any)
	node["properties"].(map[string]any)["category"] = map[string]any{"type": "string"}
	node["required"] = append(node["required"].([]string), "category")
	edge := props["edges"].(map[string]any)["items"].(map[string]any)
	edge["properties"].(map[string]any)["scopes"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	edge["required"] = append(edge["required"].([]string), "scopes")
	props["uiPreviews"] = map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
		"nodeId": map[string]any{"type": "string"}, "file": map[string]any{"type": "string"}, "label": map[string]any{"type": "string"},
		"kind": map[string]any{"type": "string", "enum": []string{"capture", "render", "reference"}}, "reason": map[string]any{"type": "string"},
		"evidence": node["properties"].(map[string]any)["evidence"],
	}, "required": []string{"nodeId", "file", "label", "kind", "reason", "evidence"}, "additionalProperties": false}}
	schema["required"] = append(schema["required"].([]string), "reviewedFiles", "deferredFiles")
	schema["required"] = append(schema["required"].([]string), "uiPreviews", "capabilityGaps")
	props["capabilityGaps"] = map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
		"kind":    map[string]any{"type": "string", "enum": []string{"adapter", "scanner", "preview"}},
		"summary": map[string]any{"type": "string"}, "proposal": map[string]any{"type": "string"}, "verification": map[string]any{"type": "string"}, "evidence": node["properties"].(map[string]any)["evidence"],
	}, "required": []string{"kind", "summary", "proposal", "verification", "evidence"}, "additionalProperties": false}}
	return schema
}
func analysisBatches(a *analysisRecord, moduleID string) [][]inventoryFile {
	out := [][]inventoryFile{}
	for _, m := range a.Inventory.Modules {
		if moduleID != "" && m.ID != moduleID {
			continue
		}
		batch := []inventoryFile{}
		var size int64
		for _, f := range a.Inventory.Files {
			if f.ModuleID != m.ID || (moduleID == "" && a.Reviewed[f.Path] == f.Digest) {
				continue
			}
			if len(batch) > 0 && (len(batch) >= 12 || size+f.Size > 120000) {
				out = append(out, batch)
				batch = []inventoryFile{}
				size = 0
			}
			batch = append(batch, f)
			size += f.Size
		}
		if len(batch) > 0 {
			out = append(out, batch)
		}
	}
	return out
}
func analysisPrompt(item project, doc planDocument, batch []inventoryFile, request string) (string, map[string]bool) {
	supplied := map[string]bool{}
	var sources strings.Builder
	root, err := os.OpenRoot(item.Root)
	if err == nil {
		defer root.Close()
	}
	for _, file := range batch {
		fmt.Fprintf(&sources, "\n文件：%s（模块 %s）\n", file.Path, file.ModuleID)
		if err == nil && file.Size <= 24000 {
			data := planFile(root, file.Path)
			if data != nil && checksum(string(data)) == file.Digest {
				sources.WriteString("<project-source>\n")
				sources.Write(data)
				sources.WriteString("\n</project-source>\n")
				supplied[file.Path] = true
				continue
			}
		}
		sources.WriteString("内容未附带，请使用只读工具读取此文件后再列入 reviewedFiles。\n")
	}
	encoded := analysisCatalog(doc)
	if len(encoded) > 300000 {
		encoded = []byte(`[{"note":"目录较大，请只输出本批次节点，已有确定ID优先复用；不要猜测其他端点"}]`)
	}
	stage := "逐文件深入分析本批次，不只做概要。覆盖文件中所有有意义的页面、功能、UI事件、客户端请求、后端路由、中间件、业务组件、存储及测试。"
	if len(batch) == 0 {
		stage = "进行跨模块关系整合。根据节点目录读取必要源码，补齐 UI/功能→客户端请求→后端接口→中间件→业务组件→存储的实际连接，尤其核对请求地址和路由注册。不要重写所有节点，只返回新增/更新节点及关系；不要凭相似名字连线。"
	}
	prompt := fmt.Sprintf("目标项目：%s；目标路径：%s（不是会话工作目录）。\n%s\n用户补充：%s\n已有节点目录（仅供复用ID，不代表关系已验证）：%s\n本批源文件：%s", item.Name, item.Root, stage, request, encoded, sources.String())
	assets, _ := discoverUIAssets(item)
	media := []map[string]any{}
	for _, asset := range assets {
		media = append(media, map[string]any{"file": asset.File, "references": asset.References})
		if len(media) >= 120 {
			break
		}
	}
	encodedMedia, _ := json.Marshal(media)
	prompt += "\n可用预览素材（独立于源码覆盖）：" + string(encodedMedia)
	return prompt, supplied
}
func analysisCatalog(doc planDocument) []byte {
	catalog := []map[string]any{}
	for _, n := range doc.Nodes {
		e := n.Evidence
		if len(e) > 2 {
			e = e[:2]
		}
		catalog = append(catalog, map[string]any{"id": n.ID, "title": n.Title, "kind": n.Kind, "category": n.Category, "evidence": e})
	}
	encoded, _ := json.Marshal(catalog)
	return encoded
}

func (p *projectPlanner) executeAnalysis(ctx context.Context, cancel context.CancelFunc, item project, run projectRun) {
	defer cancel()
	defer p.releaseRun(item.ID, run.ID)
	fail := func(err error) { p.failRun(ctx, item, &run, err) }
	run.Stage = "扫描全项目源码清单"
	if err := p.saveActiveRun(ctx, item.ID, &run); err != nil {
		fail(err)
		return
	}
	a, err := p.refreshAnalysis(ctx, item)
	if err != nil {
		fail(err)
		return
	}
	if err = p.pinAnalysis(item, &run, a); err != nil {
		fail(err)
		return
	}
	if err = p.saveActiveRun(ctx, item.ID, &run); err != nil {
		fail(err)
		return
	}
	if len(a.Inventory.Files) == 0 {
		fail(errors.New("未找到可分析的源码文件，请核对扫描排除项"))
		return
	}
	if run.Analysis == "module" {
		found := false
		for _, m := range a.Inventory.Modules {
			found = found || m.ID == run.ModuleID
		}
		if !found {
			fail(errors.New("模块清单已变化，请重新选择"))
			return
		}
	}
	doc, _, _, err := p.document(item)
	if err != nil {
		fail(err)
		return
	}
	key := run.Context.Skill.Digest + ":" + strings.Join(run.Context.Skill.Adapters, ",")
	if a.SkillKey != key {
		a.Reviewed = map[string]string{}
		a.Reasons = map[string]string{}
		a.SkillKey = key
	}
	a.Integrated = ""
	if err = p.saveAnalysis(ctx, item.ID, a); err != nil {
		fail(err)
		return
	}
	batches := analysisBatches(a, run.ModuleID)
	moduleNames := map[string]string{}
	for _, module := range a.Inventory.Modules {
		moduleNames[module.ID] = module.Title
	}
	// Each batch is persisted independently. A stopped run can resume without discarding earlier work.
	for index, batch := range batches {
		if err = ctx.Err(); err != nil {
			fail(err)
			return
		}
		if run.Analysis == "full" {
			pending := []inventoryFile{}
			for _, file := range batch {
				if a.Reviewed[file.Path] != file.Digest {
					pending = append(pending, file)
				}
			}
			batch = pending
			if len(batch) == 0 {
				continue
			}
		}
		run.Stage = fmt.Sprintf("逐文件分析 %d/%d · %s · %d 个文件", index+1, len(batches), moduleNames[batch[0].ModuleID], len(batch))
		prompt, supplied := analysisPrompt(item, doc, batch, run.Prompt)
		out, turnErr := p.runTurn(ctx, item, &run, prompt, analysisSchema())
		if turnErr != nil {
			fail(turnErr)
			return
		}
		next, nextAnalysis, mergeErr := applyAnalysisReply(item, doc, a, batch, out, supplied)
		if mergeErr != nil {
			fail(mergeErr)
			return
		}
		run.Reply = fmt.Sprintf("已保存批次 %d/%d；已核对 %d/%d 个源码文件。", index+1, len(batches), nextAnalysis.view().Reviewed, len(a.Inventory.Files))
		if err = p.commitAnalysis(ctx, item.ID, &run, &next, nextAnalysis, false); err != nil {
			fail(err)
			return
		}
		doc = next
		a = nextAnalysis
	}
	run.Stage = "核对跨模块链路"
	if len(analysisCatalog(doc)) > 300000 {
		fail(errors.New("模块分析已保存，但全局节点目录超过本版整合容量。请按子项目分别管理；跨模块整合仍标为未完成。"))
		return
	}
	prompt, supplied := analysisPrompt(item, doc, nil, run.Prompt)
	out, err := p.runTurn(ctx, item, &run, prompt, analysisSchema())
	if err != nil {
		fail(err)
		return
	}
	next, nextAnalysis, err := applyAnalysisReply(item, doc, a, nil, out, supplied)
	if err != nil {
		fail(err)
		return
	}
	current, err := scanProjectInventory(ctx, item.Root)
	if err != nil {
		fail(err)
		return
	}
	nextAnalysis.Inventory = current
	if !current.Truncated && !a.Inventory.Truncated && current.Fingerprint == a.Inventory.Fingerprint {
		nextAnalysis.Integrated = current.Fingerprint
	} else {
		nextAnalysis.Integrated = ""
		nextAnalysis.Inventory.Warnings = append(nextAnalysis.Inventory.Warnings, "分析期间项目发生变化，已保留结果；继续完整分析会核对新增或变更文件。")
	}
	view := nextAnalysis.view()
	next.Summary = fmt.Sprintf("项目全景：扫描 %d 个源码文件、%d 个目录模块，Codex 已核对 %d 个文件。图谱包含 %d 个节点、%d 条关系；文件级覆盖不代表功能验收或所有运行路径均已验证。", view.Total, len(view.Modules), view.Reviewed, len(next.Nodes), len(next.Edges))
	run.Stage = "本轮分析结束"
	run.Reply = next.Summary
	if view.Status != "complete" {
		run.Reply += " 仍有待分析、变更或被排除内容，请查看覆盖清单后继续。"
	}
	if err = p.commitAnalysis(ctx, item.ID, &run, &next, nextAnalysis, true); err != nil {
		fail(err)
	}
}
func applyAnalysisReply(item project, doc planDocument, a *analysisRecord, batch []inventoryFile, out turnOutput, supplied map[string]bool) (planDocument, *analysisRecord, error) {
	raw := strings.TrimSpace(out.Reply)
	if len(raw) > 512<<10 {
		return doc, a, errors.New("本批结果过大，请缩小模块")
	}
	var delta analysisReply
	if err := json.Unmarshal([]byte(raw), &delta); err != nil {
		return doc, a, errors.New("Codex 本批未返回有效分析JSON，前面已保存的批次保留")
	}
	if len(delta.Nodes) > 160 || len(delta.Edges) > 320 {
		return doc, a, errors.New("单批节点或关系超限，已有批次保留")
	}
	ids := map[string]bool{}
	for _, node := range delta.Nodes {
		if ids[node.ID] {
			return doc, a, errors.New("本批返回了重复节点ID，已有批次保留")
		}
		ids[node.ID] = true
	}
	// Never trust model supplied media URLs or persistence/provenance fields.
	modules := map[string]string{}
	for _, f := range a.Inventory.Files {
		modules[f.Path] = f.ModuleID
	}
	for i := range delta.Nodes {
		n := &delta.Nodes[i]
		n.ModuleID = ""
		n.Origin = "codex"
		n.Preview = nil
		for _, e := range n.Evidence {
			if modules[e.File] != "" {
				n.ModuleID = modules[e.File]
				break
			}
		}
	}
	merged, err := mergeProjectDocuments(doc, delta.planDocument, "codex")
	if err != nil {
		return doc, a, err
	}
	merged, err = validatePlan(item.Root, merged)
	if err != nil {
		return doc, a, err
	}
	bindings := []uiBinding{}
	if len(delta.UIPreviews) > 120 {
		if len(merged.Questions) < 512 {
			merged.Questions = append(merged.Questions, "本批预览关联超过120条，已保留源码分析，请按页面补充预览。")
		}
	} else {
		for _, candidate := range delta.UIPreviews {
			valid, previewErr := validateUIBindings(item, merged, []uiBinding{candidate})
			if previewErr != nil {
				if len(merged.Questions) < 512 {
					merged.Questions = append(merged.Questions, "预览待确认："+clipActivity(previewErr.Error(), 200))
				}
				continue
			}
			bindings = append(bindings, valid...)
		}
	}
	merged.UIPreviews = mergeUIBindings(doc.UIPreviews, bindings)
	next := *a
	next.Gaps = mergeAnalysisGaps(item, a, delta.CapabilityGaps, supplied, out.ReadFiles)
	next.Reviewed = map[string]string{}
	next.Reasons = map[string]string{}
	for k, v := range a.Reviewed {
		next.Reviewed[k] = v
	}
	for k, v := range a.Reasons {
		next.Reasons[k] = v
	}
	allowed := map[string]inventoryFile{}
	for _, file := range a.Inventory.Files {
		allowed[file.Path] = file
	}
	for _, f := range batch {
		next.Reasons[f.Path] = "本批未确认已分析，可继续重试"
	}
	root, err := os.OpenRoot(item.Root)
	if err != nil {
		return doc, a, err
	}
	defer root.Close()
	for _, file := range delta.ReviewedFiles {
		f, ok := allowed[file]
		if !ok {
			return doc, a, fmt.Errorf("分析结果声明了不属于项目扫描清单的文件：%s", file)
		}
		if !supplied[file] && !out.ReadFiles[file] {
			next.Reasons[file] = "没有核对到本回合的文件读取，保留为待分析"
			continue
		}
		data := planFile(root, file)
		if data == nil || checksum(string(data)) != f.Digest {
			next.Reasons[file] = "文件在分析期间发生变化，需要重新分析"
			continue
		}
		next.Reviewed[file] = f.Digest
		delete(next.Reasons, file)
	}
	for _, deferred := range delta.DeferredFiles {
		if _, ok := allowed[deferred.File]; ok {
			next.Reasons[deferred.File] = clipActivity(deferred.Reason, 300)
			delete(next.Reviewed, deferred.File)
		}
	}
	next.Integrated = ""
	return merged, &next, nil
}
func (p *projectPlanner) commitAnalysis(ctx context.Context, projectID string, run *projectRun, doc *planDocument, a *analysisRecord, done bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plan_versions(project_id,revision,document,created_at) SELECT ?,COALESCE(MAX(revision),0)+1,?,? FROM plan_versions WHERE project_id=?`, projectID, string(raw), time.Now().Format(time.RFC3339Nano), projectID); err != nil {
		return err
	}
	for i := range a.Gaps {
		if a.Gaps[i].RunID == "" {
			a.Gaps[i].RunID = run.ID
			if run.Context != nil && run.Context.Skill != nil {
				a.Gaps[i].SkillVersion = run.Context.Skill.Version + " · " + run.Context.Skill.Digest[:12]
			}
		}
	}
	raw, err = json.Marshal(a)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO project_analysis VALUES (?,?) ON CONFLICT(project_id) DO UPDATE SET data=excluded.data`, projectID, string(raw)); err != nil {
		return err
	}
	if done {
		run.Status = "completed"
	} else {
		run.Status = "running"
	}
	run.Error = ""
	run.UpdatedAt = time.Now()
	raw, err = json.Marshal(run)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE project_runs SET status=?,thread_id=?,data=? WHERE id=? AND project_id=?`, run.Status, run.ThreadID, string(raw), run.ID, projectID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if done && p.active[projectID].id == run.ID {
		delete(p.active, projectID)
	}
	return nil
}
