package main

// A relation names a specific call site or registration. File existence alone
// is insufficient: every explicit edge must resolve its supplied source anchors.
type codeLinkSpec struct {
	From     string       `json:"from"`
	To       string       `json:"to"`
	Label    string       `json:"label"`
	Evidence []codeAnchor `json:"evidence"`
}
type codeLink struct {
	From     string         `json:"from"`
	To       string         `json:"to"`
	Label    string         `json:"label"`
	Evidence []codeLocation `json:"evidence,omitempty"`
}
type codeGraph struct {
	Nodes []codeNode `json:"nodes"`
	Edges []codeLink `json:"edges"`
}

func (idx *codeIndex) graphFor(root codeNode, specs []codeLinkSpec) *codeGraph {
	resolved := map[string]codeNode{root.Key: root}
	resolve := func(key string) bool {
		if _, ok := resolved[key]; ok {
			return true
		}
		spec, exists := idx.Nodes[key]
		if !exists || spec.Match == "" || len(resolved) >= 64 {
			return false
		}
		if spec.Kind != "后端组件" && spec.Kind != "中间件" && spec.Kind != "数据存储" && spec.Kind != "数据模型" {
			return false
		}
		node, ok := idx.node(spec)
		if !ok {
			return false
		}
		node.Key = key
		resolved[key] = node
		return true
	}
	outgoing := map[string][]codeLink{}
	seen := map[string]bool{}
	for _, spec := range specs[:min(len(specs), 128)] {
		key := spec.From + "\x00" + spec.To + "\x00" + spec.Label
		if seen[key] || spec.From == spec.To || spec.To == root.Key || spec.Label == "" || len(spec.Label) > 96 || len(spec.Evidence) == 0 || len(spec.Evidence) > 8 {
			continue
		}
		if !resolve(spec.From) || !resolve(spec.To) {
			continue
		}
		edge := codeLink{From: spec.From, To: spec.To, Label: spec.Label}
		valid := true
		for _, anchor := range spec.Evidence {
			loc, ok := idx.locate(anchor)
			if anchor.Match == "" || !ok {
				valid = false
				break
			}
			edge.Evidence = append(edge.Evidence, loc)
		}
		if !valid {
			continue
		}
		seen[key] = true
		outgoing[edge.From] = append(outgoing[edge.From], edge)
	}
	graph := &codeGraph{Nodes: []codeNode{root}, Edges: []codeLink{}}
	included := map[string]bool{root.Key: true}
	queue := []string{root.Key}
	for i := 0; i < len(queue); i++ {
		for _, edge := range outgoing[queue[i]] {
			if len(graph.Edges) >= 96 || codeGraphReaches(graph.Edges, edge.To, edge.From) {
				continue
			}
			graph.Edges = append(graph.Edges, edge)
			if !included[edge.To] {
				included[edge.To] = true
				graph.Nodes = append(graph.Nodes, resolved[edge.To])
				queue = append(queue, edge.To)
			}
		}
	}
	return graph
}

func codeGraphReaches(edges []codeLink, from, to string) bool {
	seen := map[string]bool{}
	queue := []string{from}
	for i := 0; i < len(queue); i++ {
		if queue[i] == to {
			return true
		}
		if seen[queue[i]] {
			continue
		}
		seen[queue[i]] = true
		for _, edge := range edges {
			if edge.From == queue[i] {
				queue = append(queue, edge.To)
			}
		}
	}
	return false
}

// Only locations from a validated, reachable screen graph can be read through
// the source-preview endpoint, including evidence on its connecting edges.
func codeNodeLocations(node codeNode) []codeLocation {
	locations := []codeLocation{node.codeLocation}
	locations = append(locations, node.Evidence...)
	for _, dep := range node.Uses {
		locations = append(locations, dep.codeLocation)
		locations = append(locations, dep.Evidence...)
	}
	if node.Graph != nil {
		for _, child := range node.Graph.Nodes {
			locations = append(locations, child.codeLocation)
			locations = append(locations, child.Evidence...)
		}
		for _, edge := range node.Graph.Edges {
			locations = append(locations, edge.Evidence...)
		}
	}
	return locations
}
