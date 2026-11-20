package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type projectRun struct {
	Context   *analysisContext `json:"context,omitempty"`
	ID        string           `json:"id"`
	Role      string           `json:"role"`
	NodeID    string           `json:"nodeId"`
	Title     string           `json:"title"`
	Prompt    string           `json:"prompt"`
	ThreadID  string           `json:"threadId"`
	TurnID    string           `json:"turnId"`
	Status    string           `json:"status"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
	Error     string           `json:"error"`
	Reply     string           `json:"reply"`
	Analysis  string           `json:"analysis,omitempty"`
	ModuleID  string           `json:"moduleId,omitempty"`
	Stage     string           `json:"stage,omitempty"`
}
type planState struct {
	AnalysisContext *analysisContext `json:"analysisContext,omitempty"`
	Document        planDocument     `json:"document"`
	Revision        int              `json:"revision"`
	Source          string           `json:"source"`
	UpdatedAt       string           `json:"updatedAt"`
	PlannerThreadID string           `json:"plannerThreadId"`
	Runs            []projectRun     `json:"runs"`
	Analysis        *analysisView    `json:"analysis,omitempty"`
	UI              *projectUIView   `json:"ui,omitempty"`
	Connection      struct {
		Available bool   `json:"available"`
		Message   string `json:"message"`
	} `json:"connection"`
}
type startRunInput struct {
	Role          string `json:"role"`
	Prompt        string `json:"prompt"`
	NodeID        string `json:"nodeId"`
	ContinueRunID string `json:"continueRunId"`
	RequestID     string `json:"requestId"`
	Analysis      string `json:"analysis"`
	ModuleID      string `json:"moduleId"`
}
type activeProjectRun struct {
	id     string
	cancel context.CancelFunc
}
type projectPlanner struct {
	analysisCwd string
	skillRoot   string
	db          *sql.DB
	projects    *projectRegistry
	binary      string
	mu          sync.Mutex
	workers     sync.WaitGroup
	closing     bool
	active      map[string]activeProjectRun
	openRPC     func(context.Context, string, string, func(rpcEnvelope)) (*codexRPC, error)
}

func newProjectPlanner(db *sql.DB, projects *projectRegistry, binary string) (*projectPlanner, error) {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS plan_heads(project_id TEXT PRIMARY KEY,thread_id TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS analysis_heads(project_id TEXT PRIMARY KEY,cwd TEXT NOT NULL,skill_key TEXT NOT NULL,thread_id TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS plan_versions(project_id TEXT NOT NULL,revision INTEGER NOT NULL,document TEXT NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(project_id,revision));
 CREATE TABLE IF NOT EXISTS project_runs(id TEXT PRIMARY KEY,project_id TEXT NOT NULL,role TEXT NOT NULL,node_id TEXT NOT NULL,thread_id TEXT NOT NULL,status TEXT NOT NULL,created_at TEXT NOT NULL,data TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS project_runs_by_project ON project_runs(project_id,created_at);
 CREATE TABLE IF NOT EXISTS project_run_events(run_id TEXT NOT NULL,item_id TEXT NOT NULL,at TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(run_id,item_id));
 CREATE TABLE IF NOT EXISTS project_analysis(project_id TEXT PRIMARY KEY,data TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS project_ui_bindings(project_id TEXT NOT NULL,node_id TEXT NOT NULL,asset_id TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(project_id,node_id,asset_id));
 CREATE TABLE IF NOT EXISTS project_ui_captures(project_id TEXT NOT NULL,id TEXT NOT NULL,label TEXT NOT NULL,mime TEXT NOT NULL,created_at TEXT NOT NULL,data BLOB NOT NULL,PRIMARY KEY(project_id,id));`)
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	p := &projectPlanner{analysisCwd: canonicalCwd(cwd), skillRoot: filepath.Join(os.TempDir(), "project-map-analysis-skills"), db: db, projects: projects, binary: binary, active: map[string]activeProjectRun{}, openRPC: openCodexRPC}
	rows, err := db.Query(`SELECT project_id,data FROM project_runs WHERE status IN ('starting','running')`)
	if err != nil {
		return nil, err
	}
	type abandoned struct {
		project string
		run     projectRun
	}
	pending := []abandoned{}
	for rows.Next() {
		var id, raw string
		if rows.Scan(&id, &raw) != nil {
			continue
		}
		var run projectRun
		if json.Unmarshal([]byte(raw), &run) == nil {
			pending = append(pending, abandoned{id, run})
		}
	}
	rows.Close()
	for _, a := range pending {
		a.run.Status = "interrupted"
		a.run.Error = "工作台连接已重启，之前任务的结果需要核对。"
		if err = p.saveRun(a.project, &a.run); err != nil {
			return nil, err
		}
	}
	return p, nil
}
func (p *projectPlanner) register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/projects/{id}/workbench", p.get)
	mux.HandleFunc("POST /api/projects/{id}/runs", p.start)
	mux.HandleFunc("POST /api/projects/{id}/runs/{run}/interrupt", p.interrupt)
	mux.HandleFunc("GET /api/projects/{id}/plan-source", p.source)
	mux.HandleFunc("GET /api/projects/{id}/plan-versions", p.versions)
	mux.HandleFunc("POST /api/projects/{id}/analysis/scan", p.scan)
	mux.HandleFunc("GET /api/projects/{id}/app-ui", p.appUI)
	mux.HandleFunc("GET /api/projects/{id}/ui-assets", p.uiAssets)
	mux.HandleFunc("GET /api/projects/{id}/ui-asset/{asset}", p.uiAsset)
	mux.HandleFunc("POST /api/projects/{id}/ui-preview", p.bindUI)
	mux.HandleFunc("DELETE /api/projects/{id}/ui-preview", p.unbindUI)
	mux.HandleFunc("POST /api/projects/{id}/ui-capture", p.uploadUI)
}
func (p *projectPlanner) document(item project) (planDocument, int, string, error) {
	var raw, at string
	var revision int
	err := p.db.QueryRow(`SELECT document,revision,created_at FROM plan_versions WHERE project_id=? ORDER BY revision DESC LIMIT 1`, item.ID).Scan(&raw, &revision, &at)
	missing := err == sql.ErrNoRows
	if err != nil && !missing {
		return planDocument{}, 0, "", err
	}
	var doc planDocument
	if missing {
		doc = scanPlan(item)
	} else if err = json.Unmarshal([]byte(raw), &doc); err != nil {
		return doc, 0, "", err
	}
	analysis, err := p.analysis(item.ID)
	if err != nil {
		return doc, 0, "", err
	}
	if analysis != nil {
		if missing {
			doc = planDocument{Kind: item.Kind, Stack: item.Stack, Nodes: []planNode{}, Edges: []planEdge{}, Questions: []string{}}
		}
		doc = overlayInventory(item, doc, analysis)
	} else {
		// Existing App mappings remain available before the first inventory scan.
		doc = overlayInventory(item, doc, &analysisRecord{})
	}
	if len(doc.Nodes) == 0 {
		return doc, revision, at, nil
	}
	doc, err = validatePlan(item.Root, doc)
	return doc, revision, at, err
}
func (p *projectPlanner) runs(projectID string) ([]projectRun, error) {
	rows, err := p.db.Query(`SELECT data FROM project_runs WHERE project_id=? ORDER BY created_at DESC LIMIT 24`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []projectRun{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var run projectRun
		if err = json.Unmarshal([]byte(raw), &run); err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}
func (p *projectPlanner) get(w http.ResponseWriter, r *http.Request) {
	item, err := p.projects.project(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	doc, revision, at, err := p.document(item)
	if err != nil {
		http.Error(w, "无法读取项目档案", 500)
		return
	}
	runs, err := p.runs(item.ID)
	if err != nil {
		http.Error(w, "无法读取任务记录", 500)
		return
	}
	state := planState{Document: doc, Revision: revision, UpdatedAt: at, Source: "scan", Runs: runs}
	state.UI, err = p.attachUI(item, &state.Document)
	if err != nil {
		http.Error(w, "无法读取界面预览", 500)
		return
	}
	analysis, err := p.analysis(item.ID)
	if err != nil {
		http.Error(w, "无法读取分析覆盖清单", 500)
		return
	}
	state.Analysis = analysis.view()
	state.AnalysisContext = p.analysisContext(item, analysis)
	for i := range state.Runs {
		if state.Runs[i].Context == nil {
			state.Runs[i].Context = &analysisContext{Owner: "project", Cwd: item.Root, TargetRoot: item.Root}
		}
	}
	if revision > 0 {
		state.Source = "codex"
	}
	state.PlannerThreadID = p.analysisHead(item, state.AnalysisContext)
	_, err = exec.LookPath(p.binary)
	state.Connection.Available = err == nil
	state.Connection.Message = "Codex CLI 可用，发起任务时连接本机 Codex。"
	if err != nil {
		state.Connection.Message = "未找到 Codex CLI，请配置服务的 -codex-bin 路径。"
	}
	writeJSON(w, state)
}
func (p *projectPlanner) saveRun(projectID string, run *projectRun) error {
	run.UpdatedAt = time.Now()
	raw, err := json.Marshal(run)
	if err != nil {
		return err
	}
	_, err = p.db.Exec(`INSERT INTO project_runs VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET thread_id=excluded.thread_id,status=excluded.status,data=excluded.data`, run.ID, projectID, run.Role, run.NodeID, run.ThreadID, run.Status, run.CreatedAt.Format(time.RFC3339Nano), string(raw))
	return err
}
func decodeRunInput(w http.ResponseWriter, r *http.Request) (startRunInput, error) {
	var input startRunInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 20000))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		return input, err
	}
	if dec.Decode(new(any)) != io.EOF {
		return input, errors.New("请求格式不正确")
	}
	input.Prompt = strings.TrimSpace(input.Prompt)
	if (input.Role != "plan" && input.Role != "execute") || len(input.Prompt) > 12000 || (input.Role == "execute" && (input.NodeID == "" || input.Prompt == "")) {
		return input, errors.New("请填写有效的任务要求")
	}
	if input.RequestID != "" && !taskID.MatchString(input.RequestID) {
		return input, errors.New("请求标识不正确")
	}
	if input.Analysis != "" && (input.Role != "plan" || (input.Analysis != "full" && input.Analysis != "module") || (input.Analysis == "module" && !taskID.MatchString(input.ModuleID)) || input.ContinueRunID != "") {
		return input, errors.New("分析范围无效")
	}
	if input.Analysis != "module" && input.ModuleID != "" {
		return input, errors.New("模块范围无效")
	}
	return input, nil
}
func (p *projectPlanner) start(w http.ResponseWriter, r *http.Request) {
	input, err := decodeRunInput(w, r)
	if err != nil {
		http.Error(w, "任务要求无效或过长", 400)
		return
	}
	item, err := p.projects.project(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err = exec.LookPath(p.binary); err != nil {
		http.Error(w, "未找到 Codex CLI，请设置 -codex-bin", 503)
		return
	}
	doc, _, _, err := p.document(item)
	if err != nil {
		http.Error(w, "无法读取项目档案", 500)
		return
	}
	var selected *planNode
	for i := range doc.Nodes {
		if doc.Nodes[i].ID == input.NodeID {
			selected = &doc.Nodes[i]
		}
	}
	if input.Role == "execute" && selected == nil {
		http.Error(w, "功能卡片已变化，请刷新后重试", 409)
		return
	}
	now := time.Now()
	buf := make([]byte, 12)
	if _, err = rand.Read(buf); err != nil {
		http.Error(w, "无法创建任务", 500)
		return
	}
	id := hex.EncodeToString(buf)
	if input.RequestID != "" {
		id = checksum(item.ID + ":" + input.RequestID)
	}
	run := projectRun{ID: id, Role: input.Role, NodeID: input.NodeID, Title: "项目规划", Prompt: input.Prompt, Status: "starting", CreatedAt: now}
	if run.Role == "plan" {
		a, loadErr := p.analysis(item.ID)
		if loadErr != nil {
			http.Error(w, "无法读取分析上下文", 500)
			return
		}
		run.Context = p.analysisContext(item, a)
	}
	run.Analysis = input.Analysis
	run.ModuleID = input.ModuleID
	if run.Analysis != "" {
		run.Title = "项目全景分析"
	}
	if selected != nil && run.Role == "execute" {
		run.Title = selected.Title + " · 开发任务"
	}
	p.mu.Lock()
	if p.closing {
		p.mu.Unlock()
		http.Error(w, "服务正在停止，请稍后重试", http.StatusServiceUnavailable)
		return
	}
	var existing string
	if p.db.QueryRow(`SELECT data FROM project_runs WHERE id=? AND project_id=?`, id, item.ID).Scan(&existing) == nil {
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		_, _ = io.WriteString(w, existing)
		return
	}
	if _, busy := p.active[item.ID]; busy {
		p.mu.Unlock()
		http.Error(w, "此项目已有任务执行中，请等待或停止后再发起", 409)
		return
	}
	if len(p.active) >= 3 {
		p.mu.Unlock()
		http.Error(w, "已有三个项目任务执行中，请稍后重试", 429)
		return
	}
	if input.ContinueRunID != "" {
		var raw string
		err = p.db.QueryRow(`SELECT data FROM project_runs WHERE id=? AND project_id=? AND role='execute' AND node_id=?`, input.ContinueRunID, item.ID, input.NodeID).Scan(&raw)
		var prior projectRun
		if err != nil || json.Unmarshal([]byte(raw), &prior) != nil || prior.ThreadID == "" || input.Role != "execute" {
			p.mu.Unlock()
			http.Error(w, "不能继续此任务会话", 400)
			return
		}
		run.ThreadID = prior.ThreadID
	}
	if run.Role == "execute" {
		run.Context = &analysisContext{Owner: "project", Cwd: item.Root, TargetRoot: item.Root}
	}
	if err = p.saveRun(item.ID, &run); err != nil {
		p.mu.Unlock()
		http.Error(w, "无法保存任务", 500)
		return
	}
	duration := 30 * time.Minute
	if run.Analysis != "" {
		duration = 4 * time.Hour
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	p.active[item.ID] = activeProjectRun{id: run.ID, cancel: cancel}
	p.workers.Add(1)
	p.mu.Unlock()
	go func() {
		defer p.workers.Done()
		if run.Analysis != "" {
			p.executeAnalysis(ctx, cancel, item, run)
		} else {
			p.execute(ctx, cancel, item, doc, selected, run)
		}
	}()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, run)
}
func (p *projectPlanner) interrupt(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	active, ok := p.active[r.PathValue("id")]
	if ok && active.id == r.PathValue("run") {
		active.cancel()
	} else {
		ok = false
	}
	p.mu.Unlock()
	if !ok {
		http.Error(w, "该任务已结束或不属于此项目", 409)
		return
	}
	writeJSON(w, map[string]bool{"requested": true})
}
func (p *projectPlanner) source(w http.ResponseWriter, r *http.Request) {
	item, err := p.projects.project(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	doc, _, _, err := p.document(item)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	index, err := strconv.Atoi(r.URL.Query().Get("evidence"))
	if err != nil || index < 0 {
		http.NotFound(w, r)
		return
	}
	var evidence []planEvidence
	query := r.URL.Query()
	if query.Has("from") {
		for _, edge := range doc.Edges {
			if edge.From == query.Get("from") && edge.To == query.Get("to") && edge.Label == query.Get("label") {
				evidence = edge.Evidence
				break
			}
		}
	} else if edgeText := query.Get("edge"); edgeText != "" {
		edge, parseErr := strconv.Atoi(edgeText)
		if parseErr == nil && edge >= 0 && edge < len(doc.Edges) {
			evidence = doc.Edges[edge].Evidence
		}
	} else {
		for _, n := range doc.Nodes {
			if n.ID == r.URL.Query().Get("node") {
				evidence = n.Evidence
				break
			}
		}
	}
	// A newly committed batch can reorder both edges and their evidence.
	if query.Has("proofFile") {
		index = len(evidence)
		for i, proof := range evidence {
			if proof.File == query.Get("proofFile") && proof.Match == query.Get("proofMatch") {
				index = i
				break
			}
		}
	}
	if index < len(evidence) {
		root, openErr := os.OpenRoot(item.Root)
		if openErr == nil {
			defer root.Close()
			start, lines, readErr := planSourceLocation(root, evidence[index])
			if readErr == nil {
				writeJSON(w, map[string]any{"file": evidence[index].File, "start": start, "lines": lines})
				return
			}
		}
	}
	http.NotFound(w, r)
}
func (p *projectPlanner) versions(w http.ResponseWriter, r *http.Request) {
	if _, err := p.projects.project(r.PathValue("id")); err != nil {
		http.NotFound(w, r)
		return
	}
	if value := r.URL.Query().Get("revision"); value != "" {
		revision, err := strconv.Atoi(value)
		if err != nil || revision < 1 {
			http.NotFound(w, r)
			return
		}
		var raw string
		if p.db.QueryRow(`SELECT document FROM plan_versions WHERE project_id=? AND revision=?`, r.PathValue("id"), revision).Scan(&raw) != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="project-plan-r%d.json"`, revision))
		_, _ = io.WriteString(w, raw)
		return
	}
	rows, err := p.db.Query(`SELECT revision,created_at FROM plan_versions WHERE project_id=? ORDER BY revision DESC LIMIT 20`, r.PathValue("id"))
	if err != nil {
		http.Error(w, "无法读取档案版本", 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var n int
		var at string
		if rows.Scan(&n, &at) == nil {
			out = append(out, map[string]any{"revision": n, "createdAt": at})
		}
	}
	writeJSON(w, out)
}

type turnOutput struct {
	Reply     string
	ReadFiles map[string]bool
}

func (p *projectPlanner) releaseRun(projectID, runID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active[projectID].id == runID {
		delete(p.active, projectID)
	}
}
func (p *projectPlanner) failRun(ctx context.Context, item project, run *projectRun, err error) {
	if run.Status != "interrupted" {
		run.Status = "failed"
	}
	run.Error = clipActivity(err.Error(), 1000)
	if ctx.Err() != nil {
		run.Status = "interrupted"
		run.Error = "任务已停止；已保存的分析批次和已有文件变更保留，可继续分析或核对结果。"
	}
	_ = p.saveRun(item.ID, run)
}
func (p *projectPlanner) execute(ctx context.Context, cancel context.CancelFunc, item project, doc planDocument, node *planNode, run projectRun) {
	defer cancel()
	defer p.releaseRun(item.ID, run.ID)
	if run.Role == "plan" {
		a, err := p.refreshAnalysis(ctx, item)
		if err != nil {
			p.failRun(ctx, item, &run, err)
			return
		}
		if err = p.pinAnalysis(item, &run, a); err != nil {
			p.failRun(ctx, item, &run, err)
			return
		}
	}
	prompt := planningPrompt(item, doc, run.Prompt)
	var schema any = planOutputSchema()
	if run.Role == "execute" {
		encoded, _ := json.Marshal(node)
		prompt = fmt.Sprintf("在当前项目 %s 中完成用户明确提出的任务。\n关联功能卡片（先核对当前源码）：%s\n用户要求：%s\n只修改实现此任务必要的项目文件，遵守项目AGENTS.md，运行适当验证。最终说明实际变更、验证命令及结果、未完成项；不要声称未验证功能已完成。不要修改工作台项目档案。", item.Name, encoded, run.Prompt)
		schema = nil
	}
	out, err := p.runTurn(ctx, item, &run, prompt, schema)
	if err != nil {
		p.failRun(ctx, item, &run, err)
		return
	}
	var nextDocument *planDocument
	if run.Role == "plan" {
		next, parseErr := parsePlanReply(out.Reply)
		if parseErr == nil {
			next, parseErr = validatePlan(item.Root, next)
		}
		if parseErr != nil {
			p.failRun(ctx, item, &run, parseErr)
			return
		}
		for _, binding := range doc.UIPreviews {
			for _, n := range next.Nodes {
				if n.ID == binding.NodeID && n.Kind == "page" {
					next.UIPreviews = append(next.UIPreviews, binding)
					break
				}
			}
		}
		run.Reply = fmt.Sprintf("已保存项目档案：%s\n共 %d 个节点、%d 条关系。源码位置已核对；功能是否通过验收仍需实际验证。", next.Summary, len(next.Nodes), len(next.Edges))
		nextDocument = &next
	}
	if err = p.complete(ctx, item.ID, &run, nextDocument); err != nil {
		p.failRun(ctx, item, &run, err)
	}
}

// Serialize progress writes with cancellation so an acknowledged stop cannot
// be followed by another starting/running snapshot from this worker.
func (p *projectPlanner) saveActiveRun(ctx context.Context, projectID string, run *projectRun) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.saveRun(projectID, run)
}
func (p *projectPlanner) runTurn(ctx context.Context, item project, run *projectRun, prompt string, schema any) (turnOutput, error) {
	run.TurnID = ""
	run.Status = "starting"
	run.Reply = ""
	out := turnOutput{ReadFiles: map[string]bool{}}
	cwd := item.Root
	if run.Context != nil {
		cwd = run.Context.Cwd
	}
	if run.Role == "plan" {
		if run.Context == nil || run.Context.Skill == nil {
			return out, errors.New("分析任务缺少工作台上下文")
		}
		text, err := pinnedSkillText(run.Context.Skill)
		if err != nil {
			return out, err
		}
		prompt = fmt.Sprintf("会话归属：工作台；会话目录：%s；只读目标项目：%s。所有相对证据路径以目标为根。\n%s\n%s", cwd, item.Root, text, prompt)
	}
	var reply string
	var terminal string
	var terminalErr string
	var storeErr error
	buffering := false
	pending := []rpcEnvelope{}
	notify := func(msg rpcEnvelope) {
		if run.TurnID == "" {
			if buffering && (msg.Method == "turn/completed" || msg.Method == "item/started" || msg.Method == "item/completed") {
				if len(pending) < 200 {
					pending = append(pending, msg)
				} else {
					storeErr = errors.New("启动回合的通知过多，无法确认事件完整性")
				}
			}
			return
		}
		if msg.Method == "turn/completed" {
			var value struct {
				ThreadID string `json:"threadId"`
				Turn     struct {
					ID     string `json:"id"`
					Status string `json:"status"`
					Error  *struct {
						Message string `json:"message"`
					} `json:"error"`
				} `json:"turn"`
			}
			if json.Unmarshal(msg.Params, &value) == nil && value.ThreadID == run.ThreadID && run.TurnID == value.Turn.ID {
				terminal = value.Turn.Status
				if value.Turn.Error != nil {
					terminalErr = value.Turn.Error.Message
				}
			}
		}
		if msg.Method != "item/started" && msg.Method != "item/completed" {
			return
		}
		var value struct {
			ThreadID  string          `json:"threadId"`
			TurnID    string          `json:"turnId"`
			Started   int64           `json:"startedAtMs"`
			Completed int64           `json:"completedAtMs"`
			Item      json.RawMessage `json:"item"`
		}
		if json.Unmarshal(msg.Params, &value) != nil || value.ThreadID != run.ThreadID {
			return
		}
		if value.TurnID != run.TurnID {
			return
		}
		var info struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			Text  string `json:"text"`
			Phase string `json:"phase"`
		}
		if json.Unmarshal(value.Item, &info) != nil {
			return
		}
		if info.Type == "agentMessage" && msg.Method == "item/completed" {
			reply = info.Text
			run.Reply = clipActivity(reply, 16000)
		}
		at := value.Completed
		if at == 0 {
			at = value.Started
		}
		if at == 0 {
			at = time.Now().UnixMilli()
		}
		e, ok := normalizeActivity(item.Root, observedThread{id: run.ThreadID, cwd: cwd, association: "workspace"}, observedItem{id: info.ID, turn: value.TurnID, kind: info.Type, at: at, raw: value.Item})
		if ok {
			if e.Kind == "read" && e.Status == "completed" {
				for _, file := range e.Paths {
					out.ReadFiles[file] = true
				}
			}
			e.ThreadID = checksum(run.ThreadID)
			e.TurnID = checksum(run.ThreadID + ":" + value.TurnID)
			stored := storedProjectActivity{Event: e, Proof: []string{}}
			for _, loc := range e.Locations {
				proof := []rune(loc.Text)
				if len(proof) > 16000 {
					proof = proof[:16000]
				}
				stored.Proof = append(stored.Proof, string(proof))
			}
			raw, _ := json.Marshal(stored)
			_, err := p.db.Exec(`INSERT INTO project_run_events VALUES (?,?,?,?) ON CONFLICT(run_id,item_id) DO UPDATE SET at=excluded.at,data=excluded.data`, run.ID, value.TurnID+":"+info.ID, e.At.Format(time.RFC3339Nano), string(raw))
			if err != nil {
				storeErr = err
			}
		}
		if err := p.saveActiveRun(ctx, item.ID, run); err != nil {
			storeErr = err
		}
	}
	handshakeCtx, handshakeCancel := context.WithTimeout(ctx, 20*time.Second)
	client, err := p.openRPC(handshakeCtx, p.binary, cwd, notify)
	handshakeCancel()
	if err != nil {
		return out, err
	}
	defer client.close()
	sandbox := "read-only"
	if run.Role == "execute" {
		sandbox = "workspace-write"
	}
	params := map[string]any{"cwd": cwd, "sandbox": sandbox, "approvalPolicy": "never"}
	method := "thread/start"
	if run.ThreadID != "" {
		method = "thread/resume"
		params["threadId"] = run.ThreadID
		params["excludeTurns"] = true
	}
	callCtx, callCancel := context.WithTimeout(ctx, 30*time.Second)
	result, err := client.call(callCtx, method, params, notify)
	callCancel()
	if err != nil {
		return out, err
	}
	var started struct {
		Thread struct {
			ID     string `json:"id"`
			Status struct {
				Type string `json:"type"`
			} `json:"status"`
		} `json:"thread"`
	}
	if json.Unmarshal(result, &started) != nil || started.Thread.ID == "" {
		return out, errors.New("Codex 没有返回会话标识")
	}
	if started.Thread.Status.Type == "active" {
		return out, errors.New("此会话已在其他客户端执行中，请等该回合结束后再继续")
	}
	if run.ThreadID != "" && run.ThreadID != started.Thread.ID {
		return out, errors.New("Codex 恢复的会话与项目记录不一致")
	}
	run.ThreadID = started.Thread.ID
	nameCtx, nameCancel := context.WithTimeout(ctx, 4*time.Second)
	_, _ = client.call(nameCtx, "thread/name/set", map[string]any{"threadId": run.ThreadID, "name": runThreadTitle(item, run)}, notify)
	nameCancel()
	if err = p.saveActiveRun(ctx, item.ID, run); err != nil {
		return out, err
	}
	turnParams := map[string]any{"threadId": run.ThreadID, "input": []any{map[string]any{"type": "text", "text": prompt}}}
	if run.Role == "plan" {
		turnParams["input"] = append(turnParams["input"].([]any), map[string]any{"type": "skill", "name": run.Context.Skill.Name, "path": run.Context.Skill.Path})
	}
	if schema != nil {
		turnParams["outputSchema"] = schema
	}
	buffering = true
	callCtx, callCancel = context.WithTimeout(ctx, 30*time.Second)
	result, err = client.call(callCtx, "turn/start", turnParams, notify)
	callCancel()
	if err != nil {
		client.interrupt(run.ThreadID, run.TurnID)
		return out, err
	}
	var turn struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	_ = json.Unmarshal(result, &turn)
	run.TurnID = turn.Turn.ID
	if run.TurnID == "" {
		return out, errors.New("Codex 未返回任务回合")
	}
	if err = ctx.Err(); err != nil {
		client.interrupt(run.ThreadID, run.TurnID)
		return out, err
	}
	buffering = false
	if run.Role == "plan" {
		if _, err = p.db.Exec(`INSERT INTO analysis_heads VALUES (?,?,?,?) ON CONFLICT(project_id) DO UPDATE SET cwd=excluded.cwd,skill_key=excluded.skill_key,thread_id=excluded.thread_id`, item.ID, cwd, run.Context.Skill.Digest+":"+strings.Join(run.Context.Skill.Adapters, ","), run.ThreadID); err != nil {
			client.interrupt(run.ThreadID, run.TurnID)
			return out, err
		}
	}
	run.Status = "running"
	if err = p.saveActiveRun(ctx, item.ID, run); err != nil {
		client.interrupt(run.ThreadID, run.TurnID)
		return out, err
	}
	for _, msg := range pending {
		notify(msg)
	}
	pending = nil
	for terminal == "" && storeErr == nil {
		if _, err = client.next(ctx, notify); err != nil {
			break
		}
	}
	if err != nil || storeErr != nil || ctx.Err() != nil {
		client.interrupt(run.ThreadID, run.TurnID)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if storeErr != nil {
			err = errors.New("保存操作记录失败，已请求停止任务")
		}
		return out, err
	}
	if terminal != "completed" {
		run.Status = "failed"
		if terminal == "interrupted" {
			run.Status = "interrupted"
		}
		run.Error = clipActivity(terminalErr, 1000)
		if run.Error == "" {
			run.Error = "Codex 回合未正常完成"
		}
		return out, errors.New(run.Error)
	}
	out.Reply = reply
	return out, nil
}

func (p *projectPlanner) shutdown() {
	p.mu.Lock()
	p.closing = true
	for _, run := range p.active {
		run.cancel()
	}
	p.mu.Unlock()
	p.workers.Wait()
}

// Commit the document and its successful run together. Cancellation and commit
// share the project lock, so a stop cannot be acknowledged and then overwritten.
func (p *projectPlanner) complete(ctx context.Context, projectID string, run *projectRun, doc *planDocument) error {
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
	if doc != nil {
		raw, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO plan_versions(project_id,revision,document,created_at) SELECT ?,COALESCE(MAX(revision),0)+1,?,? FROM plan_versions WHERE project_id=?`, projectID, string(raw), time.Now().Format(time.RFC3339Nano), projectID); err != nil {
			return err
		}
	}
	run.Status = "completed"
	run.Error = ""
	run.UpdatedAt = time.Now()
	raw, err := json.Marshal(run)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE project_runs SET status='completed',data=? WHERE id=? AND project_id=?`, string(raw), run.ID, projectID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if p.active[projectID].id == run.ID {
		delete(p.active, projectID)
	}
	return nil
}

func runThreadTitle(item project, run *projectRun) string {
	if run.Role == "plan" {
		return "工作台分析 · " + item.Name
	}
	return item.Name + " · " + run.Title
}
