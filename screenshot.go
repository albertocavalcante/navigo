package navigo

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"

	cdpdom "github.com/albertocavalcante/navigo/cdp/proto/dom"
	cdppage "github.com/albertocavalcante/navigo/cdp/proto/page"
)

type ScreenshotFormat string

const (
	ScreenshotFormatPNG  ScreenshotFormat = "png"
	ScreenshotFormatJPEG ScreenshotFormat = "jpeg"
	ScreenshotFormatWEBP ScreenshotFormat = "webp"
)

type ScreenshotOption func(*screenshotConfig)

type screenshotConfig struct {
	format  ScreenshotFormat
	quality *int
}

func defaultScreenshotConfig() screenshotConfig {
	return screenshotConfig{
		format: ScreenshotFormatPNG,
	}
}

func WithScreenshotFormat(format ScreenshotFormat) ScreenshotOption {
	return func(config *screenshotConfig) {
		config.format = format
	}
}

func WithScreenshotQuality(quality int) ScreenshotOption {
	return func(config *screenshotConfig) {
		config.quality = &quality
	}
}

func (p *Page) Screenshot(ctx context.Context, opts ...ScreenshotOption) ([]byte, error) {
	config, err := applyScreenshotOptions(opts...)
	if err != nil {
		return nil, err
	}

	params, err := captureScreenshotParams(config, nil)
	if err != nil {
		return nil, err
	}

	result, err := params.Do(ctx, p.session)
	if err != nil {
		return nil, fmt.Errorf("capture page screenshot: %w", err)
	}

	data, err := decodeScreenshotData(result.Data)
	if err != nil {
		return nil, fmt.Errorf("capture page screenshot: %w", err)
	}

	return data, nil
}

func (e *Element) Screenshot(ctx context.Context, opts ...ScreenshotOption) ([]byte, error) {
	if err := e.ScrollIntoView(ctx); err != nil {
		return nil, err
	}

	config, err := applyScreenshotOptions(opts...)
	if err != nil {
		return nil, err
	}

	clip, err := screenshotClipForNode(ctx, e.page.session, e.nodeID)
	if err != nil {
		return nil, fmt.Errorf("capture element screenshot: %w", err)
	}

	params, err := captureScreenshotParams(config, clip)
	if err != nil {
		return nil, fmt.Errorf("capture element screenshot: %w", err)
	}

	result, err := params.Do(ctx, e.page.session)
	if err != nil {
		return nil, fmt.Errorf("capture element screenshot: %w", err)
	}

	data, err := decodeScreenshotData(result.Data)
	if err != nil {
		return nil, fmt.Errorf("capture element screenshot: %w", err)
	}

	return data, nil
}

func applyScreenshotOptions(opts ...ScreenshotOption) (screenshotConfig, error) {
	config := defaultScreenshotConfig()
	for _, opt := range opts {
		opt(&config)
	}

	switch config.format {
	case ScreenshotFormatPNG, ScreenshotFormatJPEG, ScreenshotFormatWEBP:
	default:
		return screenshotConfig{}, fmt.Errorf("capture screenshot: unsupported format %q", config.format)
	}

	if config.quality != nil {
		if *config.quality < 0 || *config.quality > 100 {
			return screenshotConfig{}, fmt.Errorf("capture screenshot: quality must be between 0 and 100")
		}
	}

	return config, nil
}

func captureScreenshotParams(config screenshotConfig, clip *cdppage.Viewport) (*cdppage.CaptureScreenshotParams, error) {
	format, err := screenshotFormatValue(config.format)
	if err != nil {
		return nil, err
	}

	params := cdppage.CaptureScreenshot().
		WithFormat(format).
		WithFromSurface(true)
	if clip != nil {
		params = params.WithClip(clip)
	}
	if config.quality != nil {
		params = params.WithQuality(*config.quality)
	}

	return params, nil
}

func screenshotFormatValue(format ScreenshotFormat) (cdppage.CaptureScreenshotParamsFormat, error) {
	switch format {
	case ScreenshotFormatPNG:
		return cdppage.CaptureScreenshotParamsFormatPng, nil
	case ScreenshotFormatJPEG:
		return cdppage.CaptureScreenshotParamsFormatJpeg, nil
	case ScreenshotFormatWEBP:
		return cdppage.CaptureScreenshotParamsFormatWebp, nil
	default:
		return "", fmt.Errorf("capture screenshot: unsupported format %q", format)
	}
}

func screenshotClipForNode(ctx context.Context, caller cdpdom.Caller, nodeID cdpdom.NodeID) (*cdppage.Viewport, error) {
	boxModelResult, err := cdpdom.GetBoxModel().WithNodeID(nodeID).Do(ctx, caller)
	if err != nil {
		return nil, fmt.Errorf("get box model: %w", err)
	}

	clip, ok := viewportFromBoxModel(boxModelResult.Model)
	if !ok {
		return nil, fmt.Errorf("get box model: no usable border geometry returned")
	}

	return clip, nil
}

func viewportFromBoxModel(model cdpdom.BoxModel) (*cdppage.Viewport, bool) {
	values, ok := toFloat64Slice(model.Border)
	if !ok || len(values) < 8 {
		values, ok = toFloat64Slice(model.Content)
		if !ok || len(values) < 8 {
			return nil, false
		}
	}

	minX, maxX := values[0], values[0]
	minY, maxY := values[1], values[1]
	for index := 2; index+1 < len(values); index += 2 {
		minX = math.Min(minX, values[index])
		maxX = math.Max(maxX, values[index])
		minY = math.Min(minY, values[index+1])
		maxY = math.Max(maxY, values[index+1])
	}

	width := maxX - minX
	height := maxY - minY
	if width <= 0 || height <= 0 {
		return nil, false
	}

	return &cdppage.Viewport{
		X:      minX,
		Y:      minY,
		Width:  width,
		Height: height,
		Scale:  1,
	}, true
}

func decodeScreenshotData(data string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, fmt.Errorf("decode screenshot data: %w", err)
	}

	return decoded, nil
}
