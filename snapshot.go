package navigo

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	cdpaccessibility "github.com/albertocavalcante/navigo/cdp/proto/accessibility"
)

type Snapshot struct {
	page     *Page
	nodes    []*SnapshotNode
	byID     map[string]*SnapshotNode
	byRef    map[string]*SnapshotNode
	roots    []*SnapshotNode
	markdown string

	mu      sync.Mutex
	updates <-chan struct{}
	closed  chan struct{}
	cancel  func()
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

	return newSnapshot(p, result.Nodes), nil
}

func newSnapshot(page *Page, nodes []cdpaccessibility.AXNode) *Snapshot {
	snapshot := &Snapshot{
		page:   page,
		closed: make(chan struct{}),
	}
	snapshot.reset(nodes)
	return snapshot
}

func (s *Snapshot) reset(nodes []cdpaccessibility.AXNode) {
	s.nodes = make([]*SnapshotNode, 0, len(nodes))
	s.byID = make(map[string]*SnapshotNode, len(nodes))
	s.byRef = make(map[string]*SnapshotNode)
	s.roots = s.roots[:0]

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
			s.byRef[snapshotNode.Ref] = snapshotNode
		}
		for _, property := range node.Properties {
			snapshotNode.Properties[string(property.Name)] = axValueString(&property.Value)
		}
		s.nodes = append(s.nodes, snapshotNode)
		s.byID[snapshotNode.ID] = snapshotNode
	}

	for _, node := range nodes {
		parent := s.byID[string(node.NodeID)]
		hasParent := false
		for _, maybeParent := range nodes {
			if slices.Contains(maybeParent.ChildIDs, node.NodeID) {
				if parentNode := s.byID[string(maybeParent.NodeID)]; parentNode != nil {
					parentNode.Children = append(parentNode.Children, parent)
					hasParent = true
					break
				}
			}
		}
		if !hasParent {
			s.roots = append(s.roots, parent)
		}
	}

	s.markdown = s.renderMarkdown()
}

func (s *Snapshot) Refresh(ctx context.Context) error {
	if s.page == nil {
		return fmt.Errorf("refresh snapshot: snapshot is not attached to a page")
	}

	result, err := cdpaccessibility.GetFullAXTree().WithDepth(-1).Do(ctx, s.page.session)
	if err != nil {
		return fmt.Errorf("refresh snapshot: %w", err)
	}

	s.reset(result.Nodes)
	return nil
}

func (s *Snapshot) WaitForUpdate(ctx context.Context) error {
	updates, err := s.ensureUpdateSubscription()
	if err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("wait for snapshot update: %w", ctx.Err())
	case <-s.closed:
		return fmt.Errorf("wait for snapshot update: snapshot closed")
	case _, ok := <-updates:
		if !ok {
			return fmt.Errorf("wait for snapshot update: update stream closed")
		}
	}

	if err := s.Refresh(ctx); err != nil {
		return err
	}

	return nil
}

func (s *Snapshot) Close() {
	s.mu.Lock()
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}

	cancel := s.cancel
	s.cancel = nil
	s.updates = nil
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

func (s *Snapshot) ensureUpdateSubscription() (<-chan struct{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	select {
	case <-s.closed:
		return nil, fmt.Errorf("wait for snapshot update: snapshot closed")
	default:
	}

	if s.updates != nil {
		return s.updates, nil
	}
	if s.page == nil {
		return nil, fmt.Errorf("wait for snapshot update: snapshot is not attached to page updates")
	}
	if s.page.isClosed() {
		return nil, fmt.Errorf("wait for snapshot update: page closed")
	}

	s.updates, s.cancel = s.page.subscribeAXUpdates()
	return s.updates, nil
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
	needle := strings.ToLower(strings.TrimSpace(role))
	for _, node := range s.nodes {
		if needle != "" && strings.Contains(strings.ToLower(node.Role), needle) {
			result = append(result, node)
		}
	}
	return result
}

func (s *Snapshot) FindByName(name string) []*SnapshotNode {
	result := make([]*SnapshotNode, 0)
	needle := strings.ToLower(strings.TrimSpace(name))
	for _, node := range s.nodes {
		if needle != "" && strings.Contains(strings.ToLower(node.Name), needle) {
			result = append(result, node)
		}
	}
	return result
}

func (s *Snapshot) InteractiveNodes() []*SnapshotNode {
	result := make([]*SnapshotNode, 0)
	for _, node := range s.nodes {
		if node.Ref == "" {
			continue
		}
		if node.Ignored {
			continue
		}
		if isInteractiveSnapshotNode(node) {
			result = append(result, node)
		}
	}
	return result
}

func (s *Snapshot) Markdown() string {
	return s.markdown
}

func (s *Snapshot) Click(ctx context.Context, ref string) error {
	element, err := s.elementForRef(ctx, ref)
	if err != nil {
		return err
	}

	if err := element.Click(ctx); err != nil {
		return fmt.Errorf("snapshot click %q: %w", ref, err)
	}

	return nil
}

func (s *Snapshot) Type(ctx context.Context, ref string, text string) error {
	element, err := s.elementForRef(ctx, ref)
	if err != nil {
		return err
	}

	if err := element.Type(ctx, text); err != nil {
		return fmt.Errorf("snapshot type %q: %w", ref, err)
	}

	return nil
}

func (s *Snapshot) Fill(ctx context.Context, ref string, text string) error {
	element, err := s.elementForRef(ctx, ref)
	if err != nil {
		return err
	}

	if err := element.Fill(ctx, text); err != nil {
		return fmt.Errorf("snapshot fill %q: %w", ref, err)
	}

	return nil
}

func (s *Snapshot) elementForRef(ctx context.Context, ref string) (*Element, error) {
	if s.page == nil {
		return nil, fmt.Errorf("snapshot action %q: snapshot is not attached to a page", ref)
	}

	node, ok := s.FindByRef(ref)
	if !ok {
		return nil, fmt.Errorf("snapshot action %q: ref not found", ref)
	}
	if node.BackendNodeID == 0 {
		return nil, fmt.Errorf("snapshot action %q: node has no backend DOM node ID", ref)
	}

	element, err := s.page.resolveBackendNodeIDToElement(ctx, node.BackendNodeID)
	if err != nil {
		return nil, fmt.Errorf("snapshot action %q: %w", ref, err)
	}

	return element, nil
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
	if state := snapshotNodeState(node); state != "" {
		builder.WriteString(" ")
		builder.WriteString(state)
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

func isInteractiveSnapshotNode(node *SnapshotNode) bool {
	if node == nil {
		return false
	}

	switch node.Role {
	case "button", "link", "textbox", "searchbox", "combobox", "checkbox", "radio", "switch", "menuitem", "option", "tab", "slider", "spinbutton":
		return true
	}

	if node.Properties["focusable"] == "true" || node.Properties["settable"] == "true" {
		return true
	}

	return false
}

func snapshotNodeState(node *SnapshotNode) string {
	if node == nil {
		return ""
	}

	parts := make([]string, 0, 5)
	for _, key := range []string{"disabled", "readonly", "required", "selected", "expanded", "checked"} {
		value := node.Properties[key]
		if value == "" || value == "false" {
			continue
		}
		if value == "true" {
			parts = append(parts, key)
			continue
		}
		parts = append(parts, key+"="+value)
	}

	if len(parts) == 0 {
		return ""
	}

	return "[" + strings.Join(parts, ", ") + "]"
}
