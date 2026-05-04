package navigo

import (
	"context"
	"fmt"
	"time"

	cdpnetwork "github.com/albertocavalcante/navigo/cdp/proto/network"
	cdpstorage "github.com/albertocavalcante/navigo/cdp/proto/storage"
)

type Cookie struct {
	Name         string
	Value        string
	Domain       string
	Path         string
	Expires      *time.Time
	Size         int
	HTTPOnly     bool
	Secure       bool
	Session      bool
	SameSite     string
	Priority     string
	SourceScheme string
	SourcePort   int
}

type CookieInput struct {
	Name         string
	Value        string
	URL          string
	Domain       string
	Path         string
	Secure       bool
	HTTPOnly     bool
	SameSite     string
	Expires      *time.Time
	Priority     string
	SourceScheme string
	SourcePort   int
}

func (b *Browser) Cookies(ctx context.Context) ([]Cookie, error) {
	result, err := cdpstorage.GetCookies().Do(ctx, b.conn.RootSession())
	if err != nil {
		return nil, fmt.Errorf("get browser cookies: %w", err)
	}

	return mapStorageCookies(result.Cookies), nil
}

func (b *Browser) SetCookies(ctx context.Context, cookies []CookieInput) error {
	params := make([]cdpstorage.CookieParam, 0, len(cookies))
	for _, cookie := range cookies {
		params = append(params, toStorageCookieParam(cookie))
	}

	if err := cdpstorage.SetCookies(params).Do(ctx, b.conn.RootSession()); err != nil {
		return fmt.Errorf("set browser cookies: %w", err)
	}

	return nil
}

func (b *Browser) ClearCookies(ctx context.Context) error {
	if err := cdpstorage.ClearCookies().Do(ctx, b.conn.RootSession()); err != nil {
		return fmt.Errorf("clear browser cookies: %w", err)
	}

	return nil
}

func (p *Page) Cookies(ctx context.Context, urls ...string) ([]Cookie, error) {
	params := cdpnetwork.GetCookies()
	if len(urls) > 0 {
		params = params.WithURLs(append([]string(nil), urls...))
	}

	result, err := params.Do(ctx, p.session)
	if err != nil {
		return nil, fmt.Errorf("get page cookies: %w", err)
	}

	return mapNetworkCookies(result.Cookies), nil
}

func mapStorageCookies(raw []cdpstorage.Cookie) []Cookie {
	cookies := make([]Cookie, 0, len(raw))
	for _, cookie := range raw {
		cookies = append(cookies, Cookie{
			Name:         cookie.Name,
			Value:        cookie.Value,
			Domain:       cookie.DOMain,
			Path:         cookie.Path,
			Expires:      timeFromEpoch(cookie.Expires),
			Size:         cookie.Size,
			HTTPOnly:     cookie.HTTPOnly,
			Secure:       cookie.Secure,
			Session:      cookie.Session,
			SameSite:     string(cookie.SameSite),
			Priority:     string(cookie.Priority),
			SourceScheme: string(cookie.SourceScheme),
			SourcePort:   cookie.SourcePort,
		})
	}
	return cookies
}

func mapNetworkCookies(raw []cdpnetwork.Cookie) []Cookie {
	cookies := make([]Cookie, 0, len(raw))
	for _, cookie := range raw {
		cookies = append(cookies, Cookie{
			Name:         cookie.Name,
			Value:        cookie.Value,
			Domain:       cookie.DOMain,
			Path:         cookie.Path,
			Expires:      timeFromEpoch(cookie.Expires),
			Size:         cookie.Size,
			HTTPOnly:     cookie.HTTPOnly,
			Secure:       cookie.Secure,
			Session:      cookie.Session,
			SameSite:     string(cookie.SameSite),
			Priority:     string(cookie.Priority),
			SourceScheme: string(cookie.SourceScheme),
			SourcePort:   cookie.SourcePort,
		})
	}
	return cookies
}

func toStorageCookieParam(cookie CookieInput) cdpstorage.CookieParam {
	param := cdpstorage.CookieParam{
		Name:         cookie.Name,
		Value:        cookie.Value,
		URL:          cookie.URL,
		DOMain:       cookie.Domain,
		Path:         cookie.Path,
		Secure:       cookie.Secure,
		HTTPOnly:     cookie.HTTPOnly,
		SameSite:     cdpstorage.CookieSameSite(cookie.SameSite),
		Priority:     cdpstorage.CookiePriority(cookie.Priority),
		SourceScheme: cdpstorage.CookieSourceScheme(cookie.SourceScheme),
		SourcePort:   cookie.SourcePort,
	}
	if cookie.Expires != nil {
		param.Expires = cdpstorage.TimeSinceEpoch(cookie.Expires.Unix())
	}
	return param
}

func timeFromEpoch(value float64) *time.Time {
	if value == 0 {
		return nil
	}

	seconds := int64(value)
	nanos := int64((value - float64(seconds)) * float64(time.Second))
	t := time.Unix(seconds, nanos).UTC()
	return &t
}
