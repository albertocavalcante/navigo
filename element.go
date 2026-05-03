package navigo

import (
	"context"
	"fmt"

	cdpdom "github.com/albertocavalcante/navigo/cdp/proto/dom"
)

type Element struct {
	page          *Page
	handle        *Handle
	nodeID        cdpdom.NodeID
	backendNodeID cdpdom.BackendNodeID
}

func (e *Element) Handle() *Handle {
	return e.handle
}

func (e *Element) NodeID() int {
	return int(e.nodeID)
}

func (e *Element) BackendNodeID() int {
	return int(e.backendNodeID)
}

func (e *Element) Release(ctx context.Context) error {
	return e.handle.Release(ctx)
}

func (e *Element) Attributes(ctx context.Context) (map[string]string, error) {
	result, err := cdpdom.GetAttributes(e.nodeID).Do(ctx, e.page.session)
	if err != nil {
		return nil, fmt.Errorf("get element attributes: %w", err)
	}

	attributes := make(map[string]string, len(result.Attributes)/2)
	for i := 0; i+1 < len(result.Attributes); i += 2 {
		attributes[result.Attributes[i]] = result.Attributes[i+1]
	}

	return attributes, nil
}

func (e *Element) Attribute(ctx context.Context, name string) (string, bool, error) {
	attributes, err := e.Attributes(ctx)
	if err != nil {
		return "", false, err
	}

	value, ok := attributes[name]
	return value, ok, nil
}

func (e *Element) Describe(ctx context.Context) (*cdpdom.Node, error) {
	result, err := cdpdom.DescribeNode().WithNodeID(e.nodeID).Do(ctx, e.page.session)
	if err != nil {
		return nil, fmt.Errorf("describe element: %w", err)
	}

	return &result.Node, nil
}

func (e *Element) OuterHTML(ctx context.Context) (string, error) {
	result, err := cdpdom.GetOuterHTML().WithNodeID(e.nodeID).Do(ctx, e.page.session)
	if err != nil {
		return "", fmt.Errorf("get element outer HTML: %w", err)
	}

	return result.OuterHTML, nil
}

func (e *Element) InnerHTML(ctx context.Context) (string, error) {
	value, err := e.handle.CallFunction(ctx, "function() { return this.innerHTML; }")
	if err != nil {
		return "", fmt.Errorf("get element inner HTML: %w", err)
	}

	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("get element inner HTML: unexpected result type %T", value)
	}

	return text, nil
}

func (e *Element) TextContent(ctx context.Context) (string, error) {
	value, err := e.handle.CallFunction(ctx, "function() { return this.textContent; }")
	if err != nil {
		return "", fmt.Errorf("get element text content: %w", err)
	}

	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("get element text content: unexpected result type %T", value)
	}

	return text, nil
}

func (e *Element) InnerText(ctx context.Context) (string, error) {
	value, err := e.handle.CallFunction(ctx, "function() { return this.innerText; }")
	if err != nil {
		return "", fmt.Errorf("get element inner text: %w", err)
	}

	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("get element inner text: unexpected result type %T", value)
	}

	return text, nil
}

func (e *Element) SetAttribute(ctx context.Context, name string, value string) error {
	if err := cdpdom.SetAttributeValue(e.nodeID, name, value).Do(ctx, e.page.session); err != nil {
		return fmt.Errorf("set element attribute: %w", err)
	}

	return nil
}

func (e *Element) Focus(ctx context.Context) error {
	if err := cdpdom.Focus().WithNodeID(e.nodeID).Do(ctx, e.page.session); err != nil {
		return fmt.Errorf("focus element: %w", err)
	}

	return nil
}

func (e *Element) ScrollIntoView(ctx context.Context) error {
	if err := cdpdom.ScrollIntoViewIfNeeded().WithNodeID(e.nodeID).Do(ctx, e.page.session); err != nil {
		return fmt.Errorf("scroll element into view: %w", err)
	}

	return nil
}

func (e *Element) Click(ctx context.Context) error {
	if err := e.ScrollIntoView(ctx); err != nil {
		return err
	}

	x, y, err := clickPointForNode(ctx, e.page.session, e.nodeID)
	if err != nil {
		return fmt.Errorf("click element: %w", err)
	}

	if err := dispatchMouseClick(ctx, e.page.session, x, y); err != nil {
		return fmt.Errorf("click element: %w", err)
	}

	return nil
}

func (e *Element) QuerySelector(ctx context.Context, selector string) (*Element, error) {
	result, err := cdpdom.QuerySelector(e.nodeID, selector).Do(ctx, e.page.session)
	if err != nil {
		return nil, fmt.Errorf("query element selector: %w", err)
	}

	if result.NodeID == 0 {
		return nil, nil
	}

	return e.page.resolveNodeIDToElement(ctx, result.NodeID)
}

func (e *Element) QuerySelectorAll(ctx context.Context, selector string) ([]*Element, error) {
	result, err := cdpdom.QuerySelectorAll(e.nodeID, selector).Do(ctx, e.page.session)
	if err != nil {
		return nil, fmt.Errorf("query element selector all: %w", err)
	}

	elements := make([]*Element, 0, len(result.NodeIDs))
	for _, nodeID := range result.NodeIDs {
		element, resolveErr := e.page.resolveNodeIDToElement(ctx, nodeID)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if element != nil {
			elements = append(elements, element)
		}
	}

	return elements, nil
}
