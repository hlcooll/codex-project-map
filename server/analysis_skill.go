package main

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The shipped skill and references are the source of truth. Each run gets an
// immutable copy, so a rebuild cannot silently change a running analysis.
//
//go:embed skills/project-analyzer
var analyzerFiles embed.FS

type analysisSkill struct {
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Digest   string   `json:"digest"`
	Adapters []string `json:"adapters"`
	Path     string   `json:"path,omitempty"`
}
type analysisContext struct {
	Owner      string         `json:"owner"`
	Cwd        string         `json:"cwd"`
	TargetRoot string         `json:"targetRoot"`
	Skill      *analysisSkill `json:"skill,omitempty"`
}
type analysisGap struct {
	ID           string         `json:"id"`
	Kind         string         `json:"kind"`
	Summary      string         `json:"summary"`
	Proposal     string         `json:"proposal"`
	Verification string         `json:"verification"`
	Evidence     []planEvidence `json:"evidence"`
	SkillVersion string         `json:"skillVersion"`
	RunID        string         `json:"runId"`
}

func analyzerBundle() (map[string][]byte, analysisSkill) {
	files := map[string][]byte{}
	_ = fs.WalkDir(analyzerFiles, "skills/project-analyzer", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files[strings.TrimPrefix(path, "skills/project-analyzer/")], _ = analyzerFiles.ReadFile(path)
		}
		return nil
	})
	version := regexp.MustCompile(`(?m)^  version: ([0-9]+\.[0-9]+\.[0-9]+)$`).FindSubmatch(files["SKILL.md"])
	meta := analysisSkill{Name: "project-analyzer", Digest: bundleDigest(files), Adapters: []string{}}
	if len(version) == 2 {
		meta.Version = string(version[1])
	}
	return files, meta
}
func bundleDigest(files map[string][]byte) string {
	keys := make([]string, 0, len(files))
	for name := range files {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, name := range keys {
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(files[name]))
		h.Write(files[name])
	}
	return hex.EncodeToString(h.Sum(nil))
}
func analysisAdapters(item project, inventory *analysisRecord) []string {
	found := map[string]bool{}
	stack := strings.ToLower(strings.Join(item.Stack, " "))
	if strings.Contains(stack, "swift") || strings.Contains(stack, "xcode") {
		found["swift"] = true
	}
	if strings.Contains(stack, "vue") || strings.Contains(stack, "react") || strings.Contains(stack, "web") {
		found["web"] = true
	}
	for _, s := range item.Stack {
		if strings.EqualFold(s, "go") {
			found["go"] = true
		}
	}
	if inventory != nil {
		for _, f := range inventory.Inventory.Files {
			switch strings.ToLower(filepath.Ext(f.Path)) {
			case ".swift", ".pbxproj", ".xcscheme":
				found["swift"] = true
			case ".vue", ".tsx", ".jsx", ".html":
				found["web"] = true
			case ".go":
				found["go"] = true
			}
		}
	}
	out := []string{}
	for _, name := range []string{"swift", "web", "go"} {
		if found[name] {
			out = append(out, name)
		}
	}
	return out
}
func (p *projectPlanner) analysisContext(item project, a *analysisRecord) *analysisContext {
	_, skill := analyzerBundle()
	skill.Adapters = analysisAdapters(item, a)
	return &analysisContext{Owner: "workbench", Cwd: p.analysisCwd, TargetRoot: item.Root, Skill: &skill}
}
func (p *projectPlanner) analysisHead(item project, c *analysisContext) string {
	var thread string
	if c == nil || c.Skill == nil {
		return ""
	}
	_ = p.db.QueryRow(`SELECT thread_id FROM analysis_heads WHERE project_id=? AND cwd=? AND skill_key=?`, item.ID, c.Cwd, c.Skill.Digest+":"+strings.Join(c.Skill.Adapters, ",")).Scan(&thread)
	return thread
}
func (p *projectPlanner) pinAnalysis(item project, run *projectRun, a *analysisRecord) error {
	c := p.analysisContext(item, a)
	files, _ := analyzerBundle()
	if c.Skill.Version == "" {
		return errors.New("分析技能缺少发布版本")
	}
	if err := os.MkdirAll(p.skillRoot, 0700); err != nil {
		return err
	}
	base := filepath.Join(p.skillRoot, c.Skill.Digest, c.Skill.Name)
	if _, err := os.Stat(base); os.IsNotExist(err) {
		tmp, err := os.MkdirTemp(p.skillRoot, ".publish-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		for name, data := range files {
			dest := filepath.Join(tmp, c.Skill.Name, name)
			if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
				return err
			}
			if err = os.WriteFile(dest, data, 0400); err != nil {
				return err
			}
		}
		if err = os.Rename(tmp, filepath.Dir(base)); err != nil {
			// Another project's run may have published the same immutable version.
			if _, statErr := os.Stat(base); statErr != nil {
				return err
			}
		}
	}
	c.Skill.Path = filepath.Join(base, "SKILL.md")
	if _, err := pinnedSkillText(c.Skill); err != nil {
		return err
	}
	run.Context = c
	run.ThreadID = p.analysisHead(item, c)
	return nil
}
func pinnedSkillText(skill *analysisSkill) (string, error) {
	if skill == nil || skill.Path == "" {
		return "", errors.New("分析技能尚未固定")
	}
	root, err := os.OpenRoot(filepath.Dir(skill.Path))
	if err != nil {
		return "", err
	}
	defer root.Close()
	files, _ := analyzerBundle()
	for name := range files {
		f, err := root.Open(name)
		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(io.LimitReader(f, 64001))
		f.Close()
		if err != nil || len(data) > 64000 {
			return "", errors.New("分析技能快照不可读，请重新发起分析")
		}
		files[name] = data
	}
	if bundleDigest(files) != skill.Digest {
		return "", errors.New("分析技能快照内容发生变化，已停止本轮分析")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "固定技能 %s v%s（%s）；启用适配器：%s。以下是本轮实际加载的技能内容。\n%s", skill.Name, skill.Version, skill.Digest, strings.Join(skill.Adapters, ", "), files["SKILL.md"])
	for _, name := range skill.Adapters {
		data, ok := files["references/"+name+".md"]
		if !ok {
			return "", errors.New("分析适配器快照缺失")
		}
		b.WriteString("\n")
		b.Write(data)
	}
	return b.String(), nil
}

func mergeAnalysisGaps(item project, a *analysisRecord, candidates []analysisGap, supplied, read map[string]bool) []analysisGap {
	out := append([]analysisGap{}, a.Gaps...)
	root, err := os.OpenRoot(item.Root)
	if err != nil {
		return out
	}
	defer root.Close()
	for i, gap := range candidates {
		if i >= 20 || (gap.Kind != "adapter" && gap.Kind != "scanner" && gap.Kind != "preview") || strings.TrimSpace(gap.Summary) == "" || strings.TrimSpace(gap.Proposal) == "" || strings.TrimSpace(gap.Verification) == "" {
			continue
		}
		proof := []planEvidence{}
		for _, e := range gap.Evidence {
			if len(proof) >= 3 {
				break
			}
			if e.Match == "" || (!supplied[e.File] && !read[e.File]) {
				continue
			}
			e = validatePlanEvidence(root, planEvidence{File: e.File, Match: e.Match})
			if e.Valid {
				proof = append(proof, e)
			}
		}
		if len(proof) == 0 {
			continue
		}
		gap.Summary = clipActivity(gap.Summary, 400)
		gap.Proposal = clipActivity(gap.Proposal, 800)
		gap.Verification = clipActivity(gap.Verification, 800)
		gap.ID = checksum(gap.Kind + ":" + gap.Summary + ":" + proof[0].File)
		gap.Evidence = proof
		gap.RunID, gap.SkillVersion = "", ""
		found := false
		for j := range out {
			if out[j].ID == gap.ID {
				out[j] = gap
				found = true
				break
			}
		}
		if !found && len(out) < 100 {
			out = append(out, gap)
		}
	}
	return out
}
