package navigo

import (
	"context"
	"errors"
	"fmt"

	cdpdom "github.com/albertocavalcante/navigo/cdp/proto/dom"
	cdpinput "github.com/albertocavalcante/navigo/cdp/proto/input"
)

func clickPointForNode(ctx context.Context, caller cdpdom.Caller, nodeID cdpdom.NodeID) (float64, float64, error) {
	quadsResult, err := cdpdom.GetContentQuads().WithNodeID(nodeID).Do(ctx, caller)
	if err != nil {
		return 0, 0, fmt.Errorf("get content quads: %w", err)
	}

	if x, y, ok := centerPointFromQuads(quadsResult.Quads); ok {
		return x, y, nil
	}

	boxModelResult, err := cdpdom.GetBoxModel().WithNodeID(nodeID).Do(ctx, caller)
	if err != nil {
		return 0, 0, fmt.Errorf("get box model: %w", err)
	}

	if x, y, ok := centerPointFromBoxModel(boxModelResult.Model); ok {
		return x, y, nil
	}

	return 0, 0, errors.New("compute click point: no usable geometry returned")
}

func dispatchMouseClick(ctx context.Context, caller cdpinput.Caller, x float64, y float64) error {
	steps := []*cdpinput.DispatchMouseEventParams{
		cdpinput.DispatchMouseEvent(cdpinput.DispatchMouseEventParamsTypeMouseMoved, x, y).
			WithButton(cdpinput.MouseButtonNone).
			WithButtons(0),
		cdpinput.DispatchMouseEvent(cdpinput.DispatchMouseEventParamsTypeMousePressed, x, y).
			WithButton(cdpinput.MouseButtonLeft).
			WithButtons(1).
			WithClickCount(1),
		cdpinput.DispatchMouseEvent(cdpinput.DispatchMouseEventParamsTypeMouseReleased, x, y).
			WithButton(cdpinput.MouseButtonLeft).
			WithButtons(1).
			WithClickCount(1),
	}

	for _, step := range steps {
		if err := step.Do(ctx, caller); err != nil {
			return fmt.Errorf("dispatch mouse click: %w", err)
		}
	}

	return nil
}

func centerPointFromQuads(quads []cdpdom.Quad) (float64, float64, bool) {
	for _, item := range quads {
		if len(item) < 8 {
			continue
		}

		return centerPointFromCoordinates(item[:8])
	}

	return 0, 0, false
}

func centerPointFromBoxModel(model cdpdom.BoxModel) (float64, float64, bool) {
	values, ok := toFloat64Slice(model.Content)
	if !ok || len(values) < 8 {
		return 0, 0, false
	}

	return centerPointFromCoordinates(values[:8])
}

func centerPointFromCoordinates(values []float64) (float64, float64, bool) {
	if len(values) < 8 {
		return 0, 0, false
	}

	var sumX float64
	var sumY float64
	for index := 0; index < 8; index += 2 {
		sumX += values[index]
		sumY += values[index+1]
	}

	return sumX / 4, sumY / 4, true
}

func toFloat64Slice(value any) ([]float64, bool) {
	if quad, ok := value.(cdpdom.Quad); ok {
		return []float64(quad), true
	}
	if values, ok := value.([]float64); ok {
		return values, true
	}
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}

	result := make([]float64, 0, len(items))
	for _, item := range items {
		number, ok := item.(float64)
		if !ok {
			return nil, false
		}
		result = append(result, number)
	}

	return result, true
}
