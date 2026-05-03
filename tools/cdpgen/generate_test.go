package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderDomain(t *testing.T) {
	t.Parallel()

	spec := protocolSpec{
		Domains: []domainSpec{
			{
				Domain: "Page",
				Commands: []commandSpec{
					{
						Name: "navigate",
						Parameters: []propertySpec{
							{Name: "url", Type: "string"},
						},
						Returns: []propertySpec{
							{Name: "frameId", Type: "string"},
							{Name: "loaderId", Type: "string", Optional: true},
						},
					},
					{
						Name: "enable",
					},
				},
				Types: []typeSpec{
					{
						ID:   "LifecycleEventName",
						Type: "string",
						Enum: []string{"DOMContentLoaded", "load"},
					},
					{
						ID:   "Frame",
						Type: "object",
						Properties: []propertySpec{
							{Name: "id", Type: "string"},
							{Name: "loaderId", Type: "string", Optional: true},
						},
					},
				},
				Events: []eventSpec{
					{
						Name: "lifecycleEvent",
						Parameters: []propertySpec{
							{Name: "frameId", Type: "string"},
							{Name: "name", Ref: "LifecycleEventName"},
						},
					},
					{
						Name: "frameNavigated",
						Parameters: []propertySpec{
							{Name: "frame", Ref: "Frame"},
						},
					},
				},
			},
		},
	}

	domain, err := findDomain(spec, "Page")
	if err != nil {
		t.Fatalf("findDomain() error = %v", err)
	}

	rendered, err := renderDomain(domain)
	if err != nil {
		t.Fatalf("renderDomain() error = %v", err)
	}

	output := string(rendered)
	for _, snippet := range []string{
		"package page",
		"CommandNavigate",
		"\"Page.navigate\"",
		"type NavigateParams struct",
		"type NavigateResult struct",
		"func Navigate(url string) *NavigateParams",
		"type LifecycleEventName string",
		"LifecycleEventNameDOMContentLoaded LifecycleEventName = \"DOMContentLoaded\"",
		"type Frame struct",
		"URL string `json:\"url\"`",
		"LoaderID string `json:\"loaderId,omitempty\"`",
		"FrameID",
		"`json:\"frameId\"`",
		"ID",
		"`json:\"id\"`",
		"func DecodeLifecycleEvent(data json.RawMessage) (*LifecycleEvent, error)",
	} {
		if !strings.Contains(output, snippet) {
			t.Fatalf("rendered output missing %q\n%s", snippet, output)
		}
	}

	if got := strings.Count(output, "type Frame struct"); got != 1 {
		t.Fatalf("strings.Count(type Frame struct) = %d, want 1\n%s", got, output)
	}
}

func TestWriteDomainPackage(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	spec := protocolSpec{
		Domains: []domainSpec{
			{
				Domain: "Browser",
				Commands: []commandSpec{
					{Name: "getVersion"},
				},
			},
		},
	}
	domain := domainSpec{
		Domain: "Browser",
		Commands: []commandSpec{
			{Name: "getVersion"},
		},
	}

	if err := writeDomainPackage(outputDir, domain, buildTypeIndex(spec)); err != nil {
		t.Fatalf("writeDomainPackage() error = %v", err)
	}

	outputPath := filepath.Join(outputDir, "browser", "browser.go")
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}

	if !strings.Contains(string(content), "CommandGetVersion") {
		t.Fatalf("generated output missing CommandGetVersion\n%s", string(content))
	}
}

func TestRenderDomainRecursiveObjectRef(t *testing.T) {
	t.Parallel()

	domain := domainSpec{
		Domain: "Page",
		Types: []typeSpec{
			{
				ID:   "Frame",
				Type: "object",
				Properties: []propertySpec{
					{Name: "id", Type: "string"},
					{
						Name: "parent",
						Ref:  "Frame",
					},
				},
			},
		},
		Events: []eventSpec{
			{
				Name: "frameNavigated",
				Parameters: []propertySpec{
					{Name: "frame", Ref: "Frame"},
				},
			},
		},
	}

	rendered, err := renderDomain(domain)
	if err != nil {
		t.Fatalf("renderDomain() error = %v", err)
	}

	output := string(rendered)
	if strings.Count(output, "type Frame struct") != 1 {
		t.Fatalf("recursive object type duplicated\n%s", output)
	}
	if !strings.Contains(output, "Parent *Frame") || !strings.Contains(output, "`json:\"parent\"`") {
		t.Fatalf("recursive field missing\n%s", output)
	}
}

func TestRenderDomainMutuallyRecursiveObjectRefsUsePointers(t *testing.T) {
	t.Parallel()

	domain := domainSpec{
		Domain: "Page",
		Types: []typeSpec{
			{
				ID:   "Frame",
				Type: "object",
				Properties: []propertySpec{
					{Name: "id", Type: "string"},
					{Name: "owner", Ref: "FrameOwner"},
				},
			},
			{
				ID:   "FrameOwner",
				Type: "object",
				Properties: []propertySpec{
					{Name: "frame", Ref: "Frame"},
				},
			},
		},
	}

	rendered, err := renderDomain(domain)
	if err != nil {
		t.Fatalf("renderDomain() error = %v", err)
	}

	output := string(rendered)
	for _, snippet := range []string{
		"Owner *FrameOwner",
		"Frame *Frame",
	} {
		if !strings.Contains(output, snippet) {
			t.Fatalf("rendered output missing %q\n%s", snippet, output)
		}
	}
}

func TestRenderDomainOptionalParamsUseWithMethods(t *testing.T) {
	t.Parallel()

	domain := domainSpec{
		Domain: "Target",
		Commands: []commandSpec{
			{
				Name: "attachToTarget",
				Parameters: []propertySpec{
					{Name: "targetId", Type: "string"},
					{Name: "flatten", Type: "boolean", Optional: true},
				},
			},
		},
	}

	rendered, err := renderDomain(domain)
	if err != nil {
		t.Fatalf("renderDomain() error = %v", err)
	}

	output := string(rendered)
	for _, snippet := range []string{
		"func AttachToTarget(targetID string) *AttachToTargetParams",
		"func (p AttachToTargetParams) WithFlatten(flatten bool) *AttachToTargetParams",
	} {
		if !strings.Contains(output, snippet) {
			t.Fatalf("rendered output missing %q\n%s", snippet, output)
		}
	}
}

func TestRenderDomainOptionalObjectUsesPointer(t *testing.T) {
	t.Parallel()

	domain := domainSpec{
		Domain: "Runtime",
		Types: []typeSpec{
			{
				ID:   "ExceptionDetails",
				Type: "object",
				Properties: []propertySpec{
					{Name: "text", Type: "string"},
				},
			},
		},
		Commands: []commandSpec{
			{
				Name: "evaluate",
				Returns: []propertySpec{
					{Name: "exceptionDetails", Ref: "ExceptionDetails", Optional: true},
				},
			},
		},
	}

	rendered, err := renderDomain(domain)
	if err != nil {
		t.Fatalf("renderDomain() error = %v", err)
	}

	if !strings.Contains(string(rendered), "ExceptionDetails *ExceptionDetails `json:\"exceptionDetails,omitempty\"`") {
		t.Fatalf("optional object field not rendered as pointer\n%s", string(rendered))
	}
}

func TestRenderDomainInlineEnumField(t *testing.T) {
	t.Parallel()

	domain := domainSpec{
		Domain: "Page",
		Events: []eventSpec{
			{
				Name: "lifecycleEvent",
				Parameters: []propertySpec{
					{Name: "frameId", Type: "string"},
					{Name: "name", Type: "string", Enum: []string{"DOMContentLoaded", "load"}},
				},
			},
		},
	}

	rendered, err := renderDomain(domain)
	if err != nil {
		t.Fatalf("renderDomain() error = %v", err)
	}

	output := string(rendered)
	for _, snippet := range []string{
		"type LifecycleEventName string",
		"LifecycleEventNameDOMContentLoaded LifecycleEventName = \"DOMContentLoaded\"",
		"Name",
		"LifecycleEventName `json:\"name\"`",
	} {
		if !strings.Contains(output, snippet) {
			t.Fatalf("rendered output missing %q\n%s", snippet, output)
		}
	}
}

func TestGenerateSeedDomainsFromRealProtocol(t *testing.T) {
	t.Parallel()

	spec, err := loadSpecs([]string{
		filepath.Join("testdata", "browser_protocol.json"),
		filepath.Join("testdata", "js_protocol.json"),
	})
	if err != nil {
		t.Fatalf("loadSpecs() error = %v", err)
	}

	outputDir := t.TempDir()
	typeIndex := buildTypeIndex(spec)
	for _, name := range []string{"Browser", "Target", "Page", "Runtime"} {
		domain, findErr := findDomain(spec, name)
		if findErr != nil {
			t.Fatalf("findDomain(%q) error = %v", name, findErr)
		}
		if writeErr := writeDomainPackage(outputDir, domain, typeIndex); writeErr != nil {
			t.Fatalf("writeDomainPackage(%q) error = %v", name, writeErr)
		}
	}

	targetOutput, err := os.ReadFile(filepath.Join(outputDir, "target", "target.go"))
	if err != nil {
		t.Fatalf("os.ReadFile(target.go) error = %v", err)
	}
	runtimeOutput, err := os.ReadFile(filepath.Join(outputDir, "runtime", "runtime.go"))
	if err != nil {
		t.Fatalf("os.ReadFile(runtime.go) error = %v", err)
	}

	for _, check := range []struct {
		output  string
		snippet string
	}{
		{output: string(targetOutput), snippet: "func AttachToTarget(targetID TargetID) *AttachToTargetParams"},
		{output: string(targetOutput), snippet: "func (p AttachToTargetParams) WithFlatten(flatten bool) *AttachToTargetParams"},
		{output: string(targetOutput), snippet: "TargetID TargetID `json:\"targetId\"`"},
		{output: string(runtimeOutput), snippet: "func Evaluate(expression string) *EvaluateParams"},
		{output: string(runtimeOutput), snippet: "func (p EvaluateParams) WithAwaitPromise(awaitPromise bool) *EvaluateParams"},
	} {
		if !strings.Contains(check.output, check.snippet) {
			t.Fatalf("generated output missing %q", check.snippet)
		}
	}
}
