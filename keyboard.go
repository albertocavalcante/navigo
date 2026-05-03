package navigo

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/albertocavalcante/navigo/cdp"
	cdpinput "github.com/albertocavalcante/navigo/cdp/proto/input"
)

type Keyboard struct {
	session *cdp.Session
}

func (p *Page) Keyboard() *Keyboard {
	return &p.keyboard
}

func (k *Keyboard) Type(ctx context.Context, text string) error {
	if err := cdpinput.InsertText(text).Do(ctx, k.session); err != nil {
		return fmt.Errorf("keyboard type: %w", err)
	}

	return nil
}

func (k *Keyboard) Press(ctx context.Context, key string) error {
	keySpec, err := keyEventSpec(key)
	if err != nil {
		return err
	}

	keyDown := cdpinput.DispatchKeyEvent(cdpinput.DispatchKeyEventParamsTypeKeyDown).
		WithKey(keySpec.key).
		WithCode(keySpec.code).
		WithWindowsVirtualKeyCode(keySpec.windowsVirtualKeyCode).
		WithNativeVirtualKeyCode(keySpec.windowsVirtualKeyCode)
	if keySpec.text != "" {
		keyDown = keyDown.WithText(keySpec.text).WithUnmodifiedText(keySpec.text)
	}
	if err := keyDown.Do(ctx, k.session); err != nil {
		return fmt.Errorf("keyboard press: %w", err)
	}

	keyUp := cdpinput.DispatchKeyEvent(cdpinput.DispatchKeyEventParamsTypeKeyUp).
		WithKey(keySpec.key).
		WithCode(keySpec.code).
		WithWindowsVirtualKeyCode(keySpec.windowsVirtualKeyCode).
		WithNativeVirtualKeyCode(keySpec.windowsVirtualKeyCode)
	if err := keyUp.Do(ctx, k.session); err != nil {
		return fmt.Errorf("keyboard press: %w", err)
	}

	return nil
}

type keySpec struct {
	key                   string
	code                  string
	text                  string
	windowsVirtualKeyCode int
}

func keyEventSpec(key string) (keySpec, error) {
	switch key {
	case "Enter":
		return keySpec{
			key:                   "Enter",
			code:                  "Enter",
			text:                  "\r",
			windowsVirtualKeyCode: 13,
		}, nil
	case "Tab":
		return keySpec{
			key:                   "Tab",
			code:                  "Tab",
			text:                  "\t",
			windowsVirtualKeyCode: 9,
		}, nil
	case "Backspace":
		return keySpec{
			key:                   "Backspace",
			code:                  "Backspace",
			windowsVirtualKeyCode: 8,
		}, nil
	case "Escape":
		return keySpec{
			key:                   "Escape",
			code:                  "Escape",
			windowsVirtualKeyCode: 27,
		}, nil
	case "ArrowLeft":
		return keySpec{
			key:                   "ArrowLeft",
			code:                  "ArrowLeft",
			windowsVirtualKeyCode: 37,
		}, nil
	case "ArrowUp":
		return keySpec{
			key:                   "ArrowUp",
			code:                  "ArrowUp",
			windowsVirtualKeyCode: 38,
		}, nil
	case "ArrowRight":
		return keySpec{
			key:                   "ArrowRight",
			code:                  "ArrowRight",
			windowsVirtualKeyCode: 39,
		}, nil
	case "ArrowDown":
		return keySpec{
			key:                   "ArrowDown",
			code:                  "ArrowDown",
			windowsVirtualKeyCode: 40,
		}, nil
	case "Delete":
		return keySpec{
			key:                   "Delete",
			code:                  "Delete",
			windowsVirtualKeyCode: 46,
		}, nil
	}

	r, size := utf8.DecodeRuneInString(key)
	if r == utf8.RuneError || size != len(key) {
		return keySpec{}, fmt.Errorf("keyboard press: unsupported key %q", key)
	}

	return keySpec{
		key:                   key,
		code:                  key,
		text:                  key,
		windowsVirtualKeyCode: int(r),
	}, nil
}
