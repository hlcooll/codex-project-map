package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"strings"
	"time"
)

type activityBinding struct {
	target    activityTarget
	line, end int
	anchor    string
}
type declarationRange struct{ start, end int }

// Swift UI files use indented declaration bodies. Only accept an unambiguous
// same-indent closing brace; multiline literals/ambiguous layouts stay file-only.
func swiftActivityEnd(lines []string, start int, anchor string) int {
	if start < 1 || start > len(lines) || !(strings.HasPrefix(anchor, "func ") || strings.HasPrefix(anchor, "struct ") || strings.HasPrefix(anchor, "class ")) {
		return 0
	}
	header := lines[start-1]
	if !strings.Contains(header, "{") || strings.Contains(header, "\"\"\"") {
		return 0
	}
	if strings.Contains(header, "}") {
		return start
	}
	indent := len(header) - len(strings.TrimLeft(header, " \t"))
	for i := start; i < len(lines); i++ {
		line := lines[i]
		trim := strings.TrimSpace(line)
		if strings.Contains(line, "\"\"\"") || strings.Contains(line, "/*") {
			return 0
		}
		if trim == "" || strings.HasPrefix(trim, "//") {
			continue
		}
		level := len(line) - len(strings.TrimLeft(line, " \t"))
		if level == indent && (trim == "}" || strings.HasPrefix(trim, "} //")) {
			return i + 1
		}
		if level <= indent {
			return 0
		}
	}
	return 0
}

func activityDeclarations(file string, lines []string) []declarationRange {
	if path.Ext(file) != ".go" {
		return nil
	}
	fset := token.NewFileSet()
	tree, err := parser.ParseFile(fset, file, strings.Join(lines, "\n"), 0)
	if err != nil {
		return nil
	}
	ranges := []declarationRange{}
	for _, decl := range tree.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			ranges = append(ranges, declarationRange{fset.Position(fn.Pos()).Line, fset.Position(fn.End()).Line})
		}
	}
	return ranges
}
func attachActivityTargets(root string, result *snapshot) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return
	}
	defer dir.Close()
	idx := openCodeIndex(dir)
	gallery := readAppUI(dir, project{Name: path.Base(root), Root: root})
	bindings := map[string][]activityBinding{}
	declarations := map[string][]declarationRange{}
	for _, group := range gallery.Groups {
		for _, screen := range group.Screens {
			nodes := idx.forScreen(group.Title, screen)
			flat := append([]codeNode{}, nodes...)
			for _, n := range nodes {
				flat = append(flat, n.Uses...)
				if n.Graph != nil {
					flat = append(flat, n.Graph.Nodes...)
				}
			}
			seen := map[string]bool{}
			for _, node := range flat {
				key := node.Key
				if key == "" {
					key = node.ID
				}
				if seen[key] {
					continue
				}
				seen[key] = true
				b := activityBinding{target: activityTarget{Group: group.Title, Screen: screen.Title, Key: key, File: node.File, Precision: "file"}, line: node.Line}
				spec := idx.Nodes[node.Key]
				b.anchor = strings.TrimSpace(spec.Match)
				if path.Ext(node.File) == ".swift" {
					b.end = swiftActivityEnd(idx.lines(node.File), node.Line, b.anchor)
				}
				if _, exists := declarations[node.File]; !exists {
					declarations[node.File] = activityDeclarations(node.File, idx.lines(node.File))
				}
				for _, r := range declarations[node.File] {
					if r.start == node.Line {
						b.end = r.end
						break
					}
				}
				bindings[node.File] = append(bindings[node.File], b)
			}
		}
	}
	for i := range result.Events {
		event := &result.Events[i]
		if event.Kind == "message" {
			continue
		}
		targets := map[string]activityTarget{}
		ordered := []string{}
		for _, loc := range event.Locations {
			for _, binding := range bindings[loc.Path] {
				exact := false
				// Declaration text is robust to shifted line numbers. A call to another
				// function is not evidence of editing that function's implementation.
				if event.Kind == "write" && binding.anchor != "" && (strings.HasPrefix(binding.anchor, "func ") || strings.HasPrefix(binding.anchor, "struct ") || strings.HasPrefix(binding.anchor, "class ")) && strings.HasPrefix(strings.TrimSpace(loc.Text), binding.anchor) {
					exact = true
				}
				// Freshness alone does not validate old line numbers. Verify the actual
				// changed text or command output against current source before using them.
				if time.Since(event.At) < 10*time.Minute && loc.Line > 0 && binding.end >= binding.line && loc.Line >= binding.line && loc.EndLine <= binding.end && verifiedActivityLines(event.Kind, loc, idx.lines(loc.Path)) {
					exact = true
				}
				target := binding.target
				if exact {
					target.Precision = "symbol"
				}
				id := target.Group + "\x00" + target.Screen + "\x00" + target.Key
				prior, exists := targets[id]
				if !exists {
					ordered = append(ordered, id)
				}
				if !exists || prior.Precision != "symbol" {
					targets[id] = target
				}
			}
		}
		// A precise symbol restricts the event to those nodes. Shared-file siblings
		// remain unlit; a file-only operation stays explicitly uncertain.
		hasExact := false
		for _, t := range targets {
			if t.Precision == "symbol" {
				hasExact = true
				break
			}
		}
		for _, id := range ordered {
			target := targets[id]
			if !hasExact || target.Precision == "symbol" {
				event.Targets = append(event.Targets, target)
			}
		}
		if hasExact {
			event.Mapping = "exact"
		} else if len(event.Targets) > 0 {
			event.Mapping = "file"
		}
	}
}

func verifiedActivityLines(kind string, loc activityLocation, lines []string) bool {
	if loc.Line < 1 || loc.EndLine < loc.Line || loc.EndLine > len(lines) || strings.TrimSpace(loc.Text) == "" {
		return false
	}
	if kind == "read" {
		text := strings.Join(lines[loc.Line-1:loc.EndLine], "\n")
		return strings.TrimSpace(text) != "" && strings.Contains(loc.Text, text)
	}
	if kind != "write" || strings.TrimSpace(lines[loc.Line-1]) != strings.TrimSpace(loc.Text) {
		return false
	}
	// Common lines (closing braces, return err, etc.) are not unique evidence.
	count := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == strings.TrimSpace(loc.Text) {
			count++
		}
	}
	return count == 1
}
