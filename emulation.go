package navigo

import (
	"context"
	"fmt"

	cdpemulation "github.com/albertocavalcante/navigo/cdp/proto/emulation"
)

type Viewport struct {
	Width             int
	Height            int
	DeviceScaleFactor float64
	Mobile            bool
	Scale             float64
}

type UserAgentOption func(*userAgentConfig)

type userAgentConfig struct {
	acceptLanguage string
	platform       string
}

type GeolocationOption func(*geolocationConfig)

type geolocationConfig struct {
	accuracy         *float64
	altitude         *float64
	altitudeAccuracy *float64
	heading          *float64
	speed            *float64
}

func (p *Page) SetViewport(ctx context.Context, viewport Viewport) error {
	if viewport.Width < 0 || viewport.Height < 0 {
		return fmt.Errorf("set viewport: invalid size %dx%d", viewport.Width, viewport.Height)
	}
	if viewport.DeviceScaleFactor < 0 {
		return fmt.Errorf("set viewport: invalid device scale factor %v", viewport.DeviceScaleFactor)
	}
	if viewport.Scale < 0 {
		return fmt.Errorf("set viewport: invalid scale %v", viewport.Scale)
	}

	params := cdpemulation.SetDeviceMetricsOverride(
		viewport.Width,
		viewport.Height,
		viewport.DeviceScaleFactor,
		viewport.Mobile,
	)
	if viewport.Scale > 0 {
		params = params.WithScale(viewport.Scale)
	}

	if err := params.Do(ctx, p.session); err != nil {
		return fmt.Errorf("set viewport: %w", err)
	}

	return nil
}

func WithAcceptLanguage(language string) UserAgentOption {
	return func(config *userAgentConfig) {
		config.acceptLanguage = language
	}
}

func WithPlatform(platform string) UserAgentOption {
	return func(config *userAgentConfig) {
		config.platform = platform
	}
}

func (p *Page) SetUserAgent(ctx context.Context, userAgent string, opts ...UserAgentOption) error {
	config := userAgentConfig{}
	for _, opt := range opts {
		opt(&config)
	}

	params := cdpemulation.SetUserAgentOverride(userAgent)
	if config.acceptLanguage != "" {
		params = params.WithAcceptLanguage(config.acceptLanguage)
	}
	if config.platform != "" {
		params = params.WithPlatform(config.platform)
	}
	if err := params.Do(ctx, p.session); err != nil {
		return fmt.Errorf("set user agent: %w", err)
	}

	return nil
}

func (p *Page) SetTimezone(ctx context.Context, timezoneID string) error {
	if err := cdpemulation.SetTimezoneOverride(timezoneID).Do(ctx, p.session); err != nil {
		return fmt.Errorf("set timezone: %w", err)
	}
	return nil
}

func WithGeolocationAccuracy(accuracy float64) GeolocationOption {
	return func(config *geolocationConfig) {
		config.accuracy = &accuracy
	}
}

func WithGeolocationAltitude(altitude float64) GeolocationOption {
	return func(config *geolocationConfig) {
		config.altitude = &altitude
	}
}

func WithGeolocationAltitudeAccuracy(accuracy float64) GeolocationOption {
	return func(config *geolocationConfig) {
		config.altitudeAccuracy = &accuracy
	}
}

func WithGeolocationHeading(heading float64) GeolocationOption {
	return func(config *geolocationConfig) {
		config.heading = &heading
	}
}

func WithGeolocationSpeed(speed float64) GeolocationOption {
	return func(config *geolocationConfig) {
		config.speed = &speed
	}
}

func (p *Page) SetGeolocation(ctx context.Context, latitude, longitude float64, opts ...GeolocationOption) error {
	config := geolocationConfig{}
	for _, opt := range opts {
		opt(&config)
	}

	params := cdpemulation.SetGeolocationOverride().
		WithLatitude(latitude).
		WithLongitude(longitude)
	if config.accuracy != nil {
		params = params.WithAccuracy(*config.accuracy)
	}
	if config.altitude != nil {
		params = params.WithAltitude(*config.altitude)
	}
	if config.altitudeAccuracy != nil {
		params = params.WithAltitudeAccuracy(*config.altitudeAccuracy)
	}
	if config.heading != nil {
		params = params.WithHeading(*config.heading)
	}
	if config.speed != nil {
		params = params.WithSpeed(*config.speed)
	}
	if err := params.Do(ctx, p.session); err != nil {
		return fmt.Errorf("set geolocation: %w", err)
	}

	return nil
}

func (p *Page) SetCPUThrottling(ctx context.Context, rate float64) error {
	if rate < 1 {
		return fmt.Errorf("set CPU throttling: invalid rate %v", rate)
	}
	if err := cdpemulation.SetCPUThrottlingRate(rate).Do(ctx, p.session); err != nil {
		return fmt.Errorf("set CPU throttling: %w", err)
	}

	return nil
}
