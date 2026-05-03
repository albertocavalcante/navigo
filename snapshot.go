package navigo

import (
	"context"
	"fmt"
	"slices"
	"strings"

	cdpaccessibility "github.com/albertocavalcante/navigo/cdp/proto/accessibility"
)

type Snapshot struct {
	nodes    []*SnapshotNode
	byID     map[string]*SnapshotNode
	byRef    map[string]*SnapshotNode
	roots    []*SnapshotNode
	markdown string
}

type SnapshotNode struct {
	ID            string
	Ref           string
	Role          string
	Name          string
	Description   string
	Value         string
	Ignored       bool
	BackendNodeID int
	FrameID       string
	Properties    map[string]string
	Children      []*SnapshotNode
}

func (p *Page) Snapshot(ctx context.Context) (*Snapshot, error) {
	result, err := cdpaccessibility.GetFullAXTree().WithDepth(-1).Do(ctx, p.session)
	if err != nil {
		return nil, fmt.Errorf("snapshot page accessibility tree: %w", err)
	}

	return newSnapshot(result.Nodes), nil
}

func newSnapshot(nodes []cdpaccessibility.AXNode) *Snapshot {
	snapshot := &Snapshot{
		nodes: make([]*SnapshotNode, 0, len(nodes)),
		byID:  make(map[string]*SnapshotNode, len(nodes)),
		byRef: make(map[string]*SnapshotNode),
	}

	for _, node := range nodes {
		snapshotNode := &SnapshotNode{
			ID:            string(node.NodeID),
			Role:          axValueString(node.Role),
			Name:          axValueString(node.Name),
			Description:   axValueString(node.Description),
			Value:         axValueString(node.Value),
			Ignored:       node.Ignored,
			BackendNodeID: node.BackendDOMNodeID,
			FrameID:       node.FrameID,
			Properties:    make(map[string]string, len(node.Properties)),
		}
		if node.BackendDOMNodeID != 0 && !node.Ignored {
			snapshotNode.Ref = fmt.Sprintf("r%d", node.BackendDOMNodeID)
			snapshot.byRef[snapshotNode.Ref] = snapshotNode
		}
		for _, property := range node.Properties {
			snapshotNode.Properties[string(property.Name)] = axValueString(&property.Value)
		}
		snapshot.nodes = append(snapshot.nodes, snapshotNode)
		snapshot.byID[snapshotNode.ID] = snapshotNode
	}

	for _, node := range nodes {
		parent := snapshot.byID[string(node.NodeID)]
		hasParent := false
		for _, maybeParent := range nodes {
			if slices.Contains(maybeParent.ChildIDs, node.NodeID) {
				if parentNode := snapshot.byID[string(maybeParent.NodeID)]; parentNode != nil {
					parentNode.Children = append(parentNode.Children, parent)
					hasParent = true
					break
				}
			}
		}
		if !hasParent {
			snapshot.roots = append(snapshot.roots, parent)
		}
	}

	snapshot.markdown = snapshot.renderMarkdown()
	return snapshot
}

func (s *Snapshot) Nodes() []*SnapshotNode {
	return append([]*SnapshotNode(nil), s.nodes...)
}

func (s *Snapshot) Roots() []*SnapshotNode {
	return append([]*SnapshotNode(nil), s.roots...)
}

func (s *Snapshot) FindByRef(ref string) (*SnapshotNode, bool) {
	node, ok := s.byRef[ref]
	return node, ok
}

func (s *Snapshot) FindByRole(role string) []*SnapshotNode {
	result := make([]*SnapshotNode, 0)
	for _, node := range s.nodes {
		if node.Role == role {
			result = append(result, node)
		}
	}
	return result
}

func (s *Snapshot) FindByName(name string) []*SnapshotNode {
	result := make([]*SnapshotNode, 0)
	for _, node := range s.nodes {
		if node.Name == name {
			result = append(result, node)
		}
	}
	return result
}

func (s *Snapshot) Markdown() string {
	return s.markdown
}

func (s *Snapshot) renderMarkdown() string {
	var builder strings.Builder
	for _, root := range s.roots {
		writeSnapshotNode(&builder, root, 0)
	}
	return strings.TrimSpace(builder.String())
}

func writeSnapshotNode(builder *strings.Builder, node *SnapshotNode, depth int) {
	if node == nil {
		return
	}

	indent := strings.Repeat("  ", depth)
	builder.WriteString(indent)
	builder.WriteString("- ")
	builder.WriteString(node.Role)
	if node.Name != "" {
		builder.WriteString(` "`)
		builder.WriteString(node.Name)
		builder.WriteString(`"`)
	}
	if node.Ref != "" {
		builder.WriteString(" [")
		builder.WriteString(node.Ref)
		builder.WriteString("]")
	}
	if node.Value != "" {
		builder.WriteString(" = ")
		builder.WriteString(node.Value)
	}
	builder.WriteString("\n")

	for _, child := range node.Children {
		writeSnapshotNode(builder, child, depth+1)
	}
}

func axValueString(value *cdpaccessibility.AXValue) string {
	if value == nil || value.Value == nil {
		return ""
	}
	return fmt.Sprint(value.Value)
}
