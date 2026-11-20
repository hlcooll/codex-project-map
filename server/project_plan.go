package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
	"unicode/utf8"
)

type planEvidence struct {
	File   string `json:"file"`
	Match  string `json:"match"`
	Line   int    `json:"line"`
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
	Digest string `json:"digest,omitempty"`
}
type planNode struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Kind        string         `json:"kind"`
	Description string         `json:"description"`
	State       string         `json:"state"`
	Evidence    []planEvidence `json:"evidence"`
	Acceptance  []string       `json:"acceptance"`
	ModuleID    string         `json:"moduleId,omitempty"`
	Category    string         `json:"category,omitempty"`
	Origin      string         `json:"origin,omitempty"`
	Preview     *planPreview   `json:"preview,omitempty"`
}
type planPreview struct {
	Group    string        `json:"group"`
	Screen   string        `json:"screen"`
	Captures []planCapture `json:"captures"`
	Demo     string        `json:"demo,omitempty"`
}
type planCapture struct {
	Label   string `json:"label"`
	URL     string `json:"url"`
	Kind    string `json:"kind,omitempty"`
	Source  string `json:"source,omitempty"`
	AssetID string `json:"assetId,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Mode    string `json:"mode,omitempty"`
}
type planEdge struct {
	From     string         `json:"from"`
	To       string         `json:"to"`
	Label    string         `json:"label"`
	Evidence []planEvidence `json:"evidence"`
	Origin   string         `json:"origin,omitempty"`
	Scopes   []string       `json:"scopes,omitempty"`
}
type planDocument struct {
	Summary    string      `json:"summary"`
	Kind       string      `json:"kind"`
	Stack      []string    `json:"stack"`
	Nodes      []planNode  `json:"nodes"`
	Edges      []planEdge  `json:"edges"`
	Questions  []string    `json:"questions"`
	UIPreviews []uiBinding `json:"uiPreviews,omitempty"`
}

var planKinds = map[string]bool{"module": true, "feature": true, "page": true, "api": true, "component": true, "storage": true, "test": true, "file": true}

func planPath(file string) bool {
	if file == "" || file == "." || path.IsAbs(file) || path.Clean(file) != file || strings.ContainsAny(file, "\\\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(file, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}
func planFile(root *os.Root, file string) []byte {
	if !planPath(file) {
		return nil
	}
	ext := strings.ToLower(path.Ext(file))
	allowed := codeExtensions[ext] || ext == ".md" || ext == ".json" || ext == ".yaml" || ext == ".yml" || ext == ".toml" || ext == ".html" || ext == ".css" || ext == ".sh" || file == "go.mod" || path.Base(file) == "Dockerfile" || path.Base(file) == "Makefile" || ext == ".txt"
	if !allowed {
		return nil
	}
	data := readOverviewFile(root, file, 768<<10)
	if !utf8.Valid(data) {
		return nil
	}
	return data
}
func validatePlanEvidence(root *os.Root, e planEvidence) planEvidence {
	prior := e.Digest
	e.Valid = false
	e.Line = 0
	e.Reason = "源码位置尚未核对"
	if !planPath(e.File) {
		e.Reason = "路径不属于可展示的项目源码"
		return e
	}
	info, err := root.Stat(e.File)
	if err != nil {
		e.Reason = "文件或目录不存在"
		return e
	}
	if info.IsDir() {
		if e.Match == "" {
			e.Valid = true
			e.Reason = ""
		}
		return e
	}
	data := planFile(root, e.File)
	if data == nil {
		e.Reason = "文件不可读取或类型暂不支持"
		return e
	}
	digest := checksum(string(data))
	e.Digest = digest
	if e.Match == "" {
		e.Valid = true
		e.Line = 1
	} else {
		matches := []int{}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, e.Match) {
				matches = append(matches, i+1)
			}
		}
		if len(matches) != 1 {
			e.Reason = "定位文字缺失或不唯一"
			return e
		}
		e.Line = matches[0]
		e.Valid = true
	}
	if prior != "" && prior != digest {
		e.Valid = false
		e.Reason = "文件已变化，请更新项目分析"
	} else {
		e.Reason = ""
	}
	return e
}
func validatePlan(rootPath string, doc planDocument) (planDocument, error) {
	if len(doc.Nodes) == 0 || len(doc.Nodes) > 4000 || len(doc.Edges) > 10000 {
		return doc, errors.New("项目图谱需要 1–4000 个节点，最多 10000 条关系；请分项目管理超大仓库")
	}
	if len(doc.Stack) > 16 || len(doc.Questions) > 512 {
		return doc, errors.New("技术栈或待确认事项过多")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return doc, err
	}
	defer root.Close()
	ids := map[string]bool{}
	doc.Summary = clipActivity(doc.Summary, 3000)
	doc.Kind = clipActivity(doc.Kind, 100)
	if doc.Stack == nil {
		doc.Stack = []string{}
	}
	if doc.Edges == nil {
		doc.Edges = []planEdge{}
	}
	if doc.Questions == nil {
		doc.Questions = []string{}
	}
	for i := range doc.Nodes {
		n := &doc.Nodes[i]
		if !taskID.MatchString(n.ID) || ids[n.ID] || !planKinds[n.Kind] || strings.TrimSpace(n.Title) == "" {
			return doc, errors.New("项目节点 ID、分类或标题不合法")
		}
		ids[n.ID] = true
		if n.State != "existing" && n.State != "planned" && n.State != "unknown" {
			return doc, errors.New("项目节点状态不合法")
		}
		if len(n.Evidence) > 10 || len(n.Acceptance) > 12 {
			return doc, errors.New("节点依据或验收条件过多")
		}
		n.Title = clipActivity(n.Title, 120)
		n.Description = clipActivity(n.Description, 1500)
		n.Category = clipActivity(n.Category, 40)
		if n.Evidence == nil {
			n.Evidence = []planEvidence{}
		}
		if n.Acceptance == nil {
			n.Acceptance = []string{}
		}
		for j, e := range n.Evidence {
			if len(e.File) > 1024 || len(e.Match) > 2000 {
				return doc, errors.New("源码依据过长")
			}
			n.Evidence[j] = validatePlanEvidence(root, e)
		}
		valid := false
		for _, e := range n.Evidence {
			valid = valid || e.Valid
		}
		if n.State == "existing" && !valid {
			n.State = "unknown"
		}
	}
	for i := range doc.Edges {
		e := &doc.Edges[i]
		if !ids[e.From] || !ids[e.To] || e.From == e.To || e.Label == "" || len(e.Evidence) > 6 {
			return doc, errors.New("关系引用了无效节点或依据")
		}
		e.Label = clipActivity(e.Label, 100)
		if len(e.Scopes) > 128 {
			return doc, errors.New("单条关系的接口范围过多")
		}
		for _, scope := range e.Scopes {
			if !ids[scope] {
				return doc, errors.New("关系的接口范围引用了不存在的节点")
			}
		}
		if e.Evidence == nil {
			e.Evidence = []planEvidence{}
		}
		for j, proof := range e.Evidence {
			e.Evidence[j] = validatePlanEvidence(root, proof)
		}
	}
	return doc, nil
}
func scanPlan(item project) planDocument {
	doc := planDocument{Summary: "先展示目录扫描结果。让 Codex 分析后，可生成业务功能、源码关系与验收条件。", Kind: item.Kind, Stack: item.Stack, Nodes: []planNode{}, Edges: []planEdge{}, Questions: []string{}}
	overview, err := inspectOverview(context.Background(), item)
	if err != nil {
		return doc
	}
	doc.Kind = overview.Kind
	doc.Stack = overview.Stack
	root, err := os.OpenRoot(item.Root)
	if err != nil {
		return doc
	}
	defer root.Close()
	for _, entry := range overview.Content {
		if len(doc.Nodes) >= 40 {
			break
		}
		kind := "file"
		if entry.Directory {
			kind = "module"
		}
		e := validatePlanEvidence(root, planEvidence{File: entry.Path})
		doc.Nodes = append(doc.Nodes, planNode{ID: "scan-" + checksum(entry.Path), Title: entry.Path, Kind: kind, Description: entry.Kind, State: "existing", Evidence: []planEvidence{e}, Acceptance: []string{}})
	}
	return doc
}
func parsePlanReply(reply string) (planDocument, error) {
	if len(reply) > 512<<10 {
		return planDocument{}, errors.New("项目档案过大，请缩小本轮分析范围")
	}
	text := strings.TrimSpace(reply)
	if strings.HasPrefix(text, "```") {
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = strings.TrimSuffix(strings.TrimSpace(text[i+1:]), "```")
		}
	}
	var doc planDocument
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &doc); err != nil {
		return doc, errors.New("规划会话未返回有效的项目档案，原有档案已保留。可继续规划重试。")
	}
	// The legacy planning schema has no media contract. Ignore extra model fields.
	doc.UIPreviews = nil
	for i := range doc.Nodes {
		doc.Nodes[i].Preview = nil
		doc.Nodes[i].Origin = "codex"
	}
	return doc, nil
}
func planOutputSchema() any {
	str := map[string]any{"type": "string"}
	arr := func(item any) any { return map[string]any{"type": "array", "items": item} }
	obj := func(props map[string]any) any {
		keys := []string{}
		for k := range props {
			keys = append(keys, k)
		}
		return map[string]any{"type": "object", "properties": props, "required": keys, "additionalProperties": false}
	}
	evidence := obj(map[string]any{"file": str, "match": str})
	node := obj(map[string]any{"id": str, "title": str, "kind": map[string]any{"type": "string", "enum": []string{"module", "feature", "page", "api", "component", "storage", "test", "file"}}, "description": str, "state": map[string]any{"type": "string", "enum": []string{"existing", "planned", "unknown"}}, "evidence": arr(evidence), "acceptance": arr(str)})
	edge := obj(map[string]any{"from": str, "to": str, "label": str, "evidence": arr(evidence)})
	return obj(map[string]any{"summary": str, "kind": str, "stack": arr(str), "nodes": arr(node), "edges": arr(edge), "questions": arr(str)})
}
func planningPrompt(item project, doc planDocument, request string) string {
	// Carry a compact, durable project model, not an unbounded conversation transcript.
	previous, _ := json.Marshal(doc)
	if len(previous) > 60000 {
		previous = []byte(`{"note":"旧档案较大，请按当前项目重新核对"}`)
	}
	return fmt.Sprintf(`请作为此项目的规划会话，读取下方目标项目根目录的真实源码并建立通用项目档案（会话目录属于工作台）。
项目：%s；根目录：%s。
只分析，不修改业务文件、不安装依赖、不启动服务、不执行开发任务。不要读取 .env、凭据或项目外无关目录。
支持任何项目类型：App、Web、后端、CLI、库、多项目目录。先识别模块，再归纳真实功能、页面/接口、组件、存储和测试。
输出必须满足 JSON Schema。建议 8–30 个有用途的节点，最多80。已有档案的节点尽量保留 id，避免反复创建同一功能。
每个已存在节点提供项目相对 evidence.file；能精确定位时提供该文件中唯一的源码单行片段 match，无法精确定位则空字符串，不编造路径或行号。
节点 state=existing 仅表示源码中存在相关实现，不表示已验收；计划新增功能用 planned，无法确认用 unknown；疑问放 questions。
关系必须引用节点id并提供源码依据；无证据的设计建议明确用“建议依赖”等文案，不能说已验证调用。
acceptance 是可验证的功能验收条件。只返回一个完整 JSON 对象，无 Markdown。
现有档案：%s
本轮用户要求：%s`, item.Name, item.Root, previous, request)
}
func planSourceLocation(root *os.Root, e planEvidence) (int, []string, error) {
	proof := validatePlanEvidence(root, planEvidence{File: e.File, Match: e.Match})
	if proof.Line < 1 {
		return 0, nil, fs.ErrNotExist
	}
	lines := strings.Split(string(planFile(root, e.File)), "\n")
	start := max(1, proof.Line-3)
	end := min(len(lines), proof.Line+22)
	out := []string{}
	for _, line := range lines[start-1 : end] {
		out = append(out, clipActivity(line, 800))
	}
	return start, out, nil
}
