package main

import (
	"context"
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

type overviewCard struct {
	Name   string   `json:"name"`
	Detail string   `json:"detail,omitempty"`
	Source string   `json:"source,omitempty"`
	Paths  []string `json:"paths"`
}

type projectContent struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Directory bool   `json:"directory"`
}

type projectOverview struct {
	Kind         string           `json:"kind"`
	Stack        []string         `json:"stack"`
	Summary      string           `json:"summary,omitempty"`
	Readme       string           `json:"readme,omitempty"`
	Architecture []overviewCard   `json:"architecture"`
	Content      []projectContent `json:"content"`
	Features     []overviewCard   `json:"features"`
	Partial      bool             `json:"partial"`
}

func (p *projectRegistry) overview(w http.ResponseWriter, r *http.Request) {
	item, err := p.project(r.PathValue("id"))
	if err != nil {
		code, message := http.StatusInternalServerError, "无法读取项目资料"
		if errors.Is(err, sql.ErrNoRows) {
			code, message = http.StatusNotFound, "项目不存在"
		}
		http.Error(w, message, code)
		return
	}
	result, err := inspectOverview(r.Context(), item)
	if err == nil {
		writeJSON(w, result)
	} else if r.Context().Err() == nil {
		http.Error(w, "项目目录不存在或无法读取", http.StatusUnprocessableEntity)
	}
}

var projectManifests = []string{
	"package.json", "go.mod", "pubspec.yaml", "settings.gradle", "settings.gradle.kts", "pyproject.toml", "requirements.txt", "Cargo.toml", "pom.xml", "index.html",
}

func projectMarkers(dir string, entries []fs.DirEntry) []string {
	markers := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if (entry.Type().IsRegular() && slices.Contains(projectManifests, name)) || (entry.IsDir() && strings.HasSuffix(name, ".xcodeproj")) {
			markers = append(markers, path.Join(dir, name))
		}
	}
	return markers
}

func skipOverviewEntry(entry fs.DirEntry) bool {
	name := strings.ToLower(entry.Name())
	if strings.HasPrefix(name, ".") || entry.Type()&os.ModeSymlink != 0 {
		return true
	}
	if !entry.IsDir() {
		return !entry.Type().IsRegular() || slices.Contains([]string{"package-lock.json", "pnpm-lock.yaml", "yarn.lock", "go.sum", "uv.lock"}, name)
	}
	return slices.Contains([]string{
		"node_modules", "vendor", "dist", "build", "bin", "out", "output", "pods", "target", "testdata", "__pycache__", "test-results",
	}, name)
}

func inspectOverview(ctx context.Context, item project) (projectOverview, error) {
	result := projectOverview{Kind: item.Kind, Stack: item.Stack, Architecture: []overviewCard{}, Content: []projectContent{}, Features: []overviewCard{}}
	root, err := os.OpenRoot(item.Root)
	if err != nil {
		return result, err
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		if skipOverviewEntry(entry) {
			continue
		}
		name := entry.Name()
		kind := "文件"
		if entry.IsDir() {
			kind = directoryRole(name)
			if kind == "" {
				kind = "目录"
			}
		} else if strings.EqualFold(name, "README.md") {
			kind = "项目说明"
			result.Readme = name
		} else if slices.Contains(projectManifests, name) {
			kind = "项目入口或配置"
		}
		result.Content = append(result.Content, projectContent{Path: name, Kind: kind, Directory: entry.IsDir()})
	}
	slices.SortStableFunc(result.Content, func(a, b projectContent) int {
		rank := func(kind string) int {
			switch kind {
			case "项目说明", "项目入口或配置":
				return 0
			case "文件":
				return 2
			default:
				return 1
			}
		}
		return rank(a.Kind) - rank(b.Kind)
	})
	if len(result.Content) > 48 {
		result.Content = result.Content[:48]
		result.Partial = true
	}

	workspace := describeArchitecture(root, entries, &result)
	if result.Readme != "" {
		readme := readOverviewFile(root, result.Readme, 128<<10)
		result.Summary, result.Features = readOverviewMarkdown(readme, result.Readme)
	}
	if workspace {
		return result, ctx.Err()
	}

	rules, _ := loadModules(item.Root)
	if len(rules) > 0 {
		result.Features = []overviewCard{}
		for _, rule := range rules {
			result.Features = append(result.Features, overviewCard{Name: rule.Name, Detail: rule.Description, Paths: rule.Paths, Source: "模块配置"})
		}
	}
	if len(result.Features) == 0 {
		var partial bool
		result.Features, partial = discoverFeatureEntries(ctx, root)
		result.Partial = result.Partial || partial
	}
	return result, ctx.Err()
}

func directoryRole(name string) string {
	switch strings.ToLower(name) {
	case "frontend", "web", "client", "ui", "ios", "android":
		return "前端"
	case "backend", "server", "api":
		return "后端"
	case "src", "app", "internal", "pkg", "cmd", "lib":
		return "源码"
	case "docs", "documentation":
		return "文档"
	case "tests", "test", "__tests__", "spec", "e2e":
		return "测试"
	case "assets", "public", "resources", "static":
		return "资源"
	case "deploy", "infra", "ops":
		return "部署"
	case "scripts", "tools":
		return "脚本与工具"
	case "migrations":
		return "数据库迁移"
	default:
		return ""
	}
}

func describeArchitecture(root *os.Root, entries []fs.DirEntry, result *projectOverview) bool {
	markers := projectMarkers("", entries)
	if result.Kind == "代码项目" {
		for _, tool := range []struct{ marker, kind, stack string }{
			{"pyproject.toml", "Python 项目", "Python"}, {"requirements.txt", "Python 项目", "Python"},
			{"Cargo.toml", "Rust 项目", "Rust"}, {"pom.xml", "Java 项目", "Maven"},
			{"index.html", "网页项目", "HTML"}, {"package.json", "JavaScript 项目", "JavaScript"},
		} {
			if slices.Contains(markers, tool.marker) {
				result.Kind = tool.kind
				result.Stack = append(result.Stack, tool.stack)
				break
			}
		}
	}
	layers := map[string][]string{}
	add := func(role string, paths ...string) {
		layers[role] = append(layers[role], paths...)
	}
	webRoot := result.Kind == "Web 应用" || slices.Contains(markers, "index.html")
	appleRoot := slices.Contains(result.Stack, "Xcode")
	for _, marker := range markers {
		switch {
		case strings.HasSuffix(marker, ".xcodeproj"):
			add("客户端", marker)
		case marker == "go.mod":
			add("Go 模块", marker)
		case webRoot && (marker == "package.json" || marker == "index.html"):
			add("前端", marker)
		case marker == "pubspec.yaml" || strings.HasPrefix(marker, "settings.gradle"):
			add("客户端", marker)
		default:
			add("项目配置", marker)
		}
	}
	subprojects := []overviewCard{}
	inspected := 0
	for _, entry := range entries {
		if !entry.IsDir() || skipOverviewEntry(entry) || strings.HasSuffix(entry.Name(), ".xcodeproj") {
			continue
		}
		name := entry.Name()
		role := directoryRole(name)
		if role == "源码" && webRoot && (name == "src" || name == "app") {
			role = "前端"
		}
		if role == "测试" || strings.HasSuffix(name, "Tests") {
			add("测试", name)
			continue
		}
		if role == "部署" || role == "数据库迁移" {
			add("部署与数据", name)
			continue
		}
		if role == "前端" || role == "后端" || role == "源码" {
			add(role, name)
		}
		// Inspect direct children for independent projects and nested app entry points.
		if inspected >= 60 {
			result.Partial = true
			continue
		}
		inspected++
		children, err := fs.ReadDir(root.FS(), name)
		if err != nil {
			result.Partial = true
			continue
		}
		childrenMarkers := projectMarkers(name, children)
		if role == "前端" || role == "后端" {
			add(role, childrenMarkers...)
		} else if appleRoot && slices.ContainsFunc(children, func(child fs.DirEntry) bool {
			return child.IsDir() && (child.Name() == "App" || child.Name() == "Features") || child.Type().IsRegular() && strings.HasSuffix(child.Name(), ".swift")
		}) {
			add("客户端", name)
		} else if len(childrenMarkers) > 0 && len(markers) == 0 {
			subprojects = append(subprojects, overviewCard{Name: name, Detail: "独立项目目录", Paths: childrenMarkers})
		}
	}
	if len(subprojects) > 0 && len(layers["前端"])+len(layers["后端"])+len(layers["客户端"]) == 0 {
		result.Kind, result.Architecture = "多项目目录", subprojects
		return true
	}
	for _, role := range []string{"前端", "客户端", "后端", "Go 模块", "源码", "项目配置", "测试", "部署与数据"} {
		if paths := layers[role]; len(paths) != 0 {
			result.Architecture = append(result.Architecture, overviewCard{Name: role, Paths: paths})
		}
	}
	return false
}

func readOverviewFile(root *os.Root, name string, limit int64) []byte {
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil
	}
	file, err := root.Open(name)
	if err != nil {
		return nil
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil
	}
	return data
}

var markdownLink = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
var markdownBullet = regexp.MustCompile(`^(?:[-*+] |\d+\. )(.+)$`)

func shortOverviewText(value string, length int) string {
	value = strings.Join(strings.Fields(value), " ")
	chars := []rune(value)
	if len(chars) > length {
		return string(chars[:length]) + "…"
	}
	return value
}

func readOverviewMarkdown(data []byte, file string) (string, []overviewCard) {
	clean := func(value string) string {
		value = markdownLink.ReplaceAllString(value, "$1")
		return strings.TrimSpace(strings.NewReplacer("**", "", "`", "").Replace(value))
	}
	features := []overviewCard{}
	summary, fence := "", ""
	intro, featureSection := true, false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			if fence == "" {
				fence = line[:3]
			} else if strings.HasPrefix(line, fence) {
				fence = ""
			}
			continue
		}
		if line == "" || fence != "" || strings.HasPrefix(line, "<") || strings.HasPrefix(line, "![") || strings.HasPrefix(line, "[![") {
			continue
		}
		if strings.HasPrefix(line, "#") {
			heading := strings.TrimLeftFunc(line, func(r rune) bool { return !unicode.IsLetter(r) })
			if strings.HasPrefix(line, "##") {
				intro = false
			}
			featureSection = slices.Contains([]string{"功能", "项目功能", "主要功能", "核心功能", "已实现", "features", "key features", "functionality"}, strings.ToLower(strings.TrimSpace(heading)))
			continue
		}
		if bullet := markdownBullet.FindStringSubmatch(line); bullet != nil {
			if !featureSection || len(features) >= 24 || strings.HasPrefix(bullet[1], "[ ]") {
				continue
			}
			text := strings.TrimSpace(strings.TrimPrefix(clean(bullet[1]), "[x]"))
			name := shortOverviewText(text, 34)
			detail := ""
			if index := strings.IndexAny(text, "：:"); index > 0 && len([]rune(text[:index])) <= 34 {
				name, detail = strings.TrimSpace(text[:index]), shortOverviewText(strings.TrimLeft(text[index:], "：:"), 180)
			} else if name != text {
				detail = shortOverviewText(text, 180)
			}
			if name != "" {
				features = append(features, overviewCard{Name: name, Detail: detail, Paths: []string{file}, Source: "项目文档"})
			}
		} else if intro && summary == "" && !strings.HasPrefix(line, "---") && !strings.HasPrefix(line, "[") {
			summary = shortOverviewText(clean(line), 240)
		}
	}
	return summary, features
}

func discoverFeatureEntries(ctx context.Context, root *os.Root) ([]overviewCard, bool) {
	features := []overviewCard{}
	seen := make(map[string]bool)
	add := func(name, file, source string) {
		if name != "" && !seen[source+":"+name] && len(features) < 24 {
			seen[source+":"+name] = true
			features = append(features, overviewCard{Name: name, Paths: []string{file}, Source: source})
		}
	}
	visited, parsed := 0, 0
	partial := false
	_ = fs.WalkDir(root.FS(), ".", func(file string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return fs.SkipAll
		}
		if err != nil {
			partial = true
			return nil
		}
		if file == "." {
			return nil
		}
		visited++
		if visited > 4000 || len(features) == 24 {
			partial = true
			return fs.SkipAll
		}
		if entry.IsDir() {
			if skipOverviewEntry(entry) || strings.Count(file, "/") >= 8 {
				return fs.SkipDir
			}
			if strings.EqualFold(path.Base(path.Dir(file)), "features") {
				add(entry.Name(), file, "功能目录")
			}
			return nil
		}
		if skipOverviewEntry(entry) {
			return nil
		}
		parent, extension := strings.ToLower(path.Base(path.Dir(file))), strings.ToLower(path.Ext(file))
		if slices.Contains([]string{"features", "pages", "views", "screens"}, parent) &&
			slices.Contains([]string{".vue", ".tsx", ".jsx", ".swift", ".svelte", ".dart", ".kt"}, extension) {
			name := strings.TrimSuffix(entry.Name(), path.Ext(file))
			if name != "_app" && name != "_document" && !strings.HasSuffix(name, ".test") && !strings.HasSuffix(name, ".spec") {
				add(name, file, "页面或视图入口")
			}
		}
		if extension == ".go" && !strings.HasSuffix(file, "_test.go") {
			if parsed >= 180 {
				partial = true
				return nil
			}
			parsed++
			for _, route := range goRoutes(readOverviewFile(root, filepath.FromSlash(file), 256<<10)) {
				add(route, file, "接口声明")
			}
		}
		return nil
	})
	return features, partial
}

var routePattern = regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS) /[^\r\n]*$`)

func goRoutes(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	file, err := parser.ParseFile(token.NewFileSet(), "", data, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	var routes []string
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		method, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (method.Sel.Name != "Handle" && method.Sel.Name != "HandleFunc") {
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if ok && literal.Kind == token.STRING {
			value, err := strconv.Unquote(literal.Value)
			if err == nil && routePattern.MatchString(value) {
				routes = append(routes, value)
			}
		}
		return true
	})
	return routes
}
