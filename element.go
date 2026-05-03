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
