package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode"
)

type protocolSpec struct {
	Version struct {
		Major string `json:"major"`
		Minor string `json:"minor"`
	} `json:"version"`
	Domains []domainSpec `json:"domains"`
}

type domainSpec struct {
	Domain   string        `json:"domain"`
	Types    []typeSpec    `json:"types"`
	Commands []commandSpec `json:"commands"`
	Events   []eventSpec   `json:"events"`
}

type typeSpec struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Enum       []string       `json:"enum"`
	Properties []propertySpec `json:"properties"`
	Items      *propertySpec  `json:"items"`
}

type commandSpec struct {
	Name       string         `json:"name"`
	Parameters []propertySpec `json:"parameters"`
	Returns    []propertySpec `json:"returns"`
}

type eventSpec struct {
	Name       string         `json:"name"`
	Parameters []propertySpec `json:"parameters"`
}

type propertySpec struct {
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	Ref         string         `json:"$ref"`
	Optional    bool           `json:"optional"`
	Enum        []string       `json:"enum"`
	Properties  []propertySpec `json:"properties"`
	Items       *propertySpec  `json:"items"`
	Description string         `json:"description"`
}

func buildTypeIndex(spec protocolSpec) map[string]typeSpec {
	index := make(map[string]typeSpec)
	for _, domain := range spec.Domains {
		for _, item := range domain.Types {
			index[item.ID] = item
			if item.ID != "" {
				index[domain.Domain+"."+item.ID] = item
			}
		}
	}

	return index
}

func loadSpec(path string) (protocolSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return protocolSpec{}, fmt.Errorf("read protocol spec: %w", err)
	}

	var spec protocolSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return protocolSpec{}, fmt.Errorf("decode protocol spec: %w", err)
	}

	return spec, nil
}

func loadSpecs(paths []string) (protocolSpec, error) {
	var merged protocolSpec
	for index, path := range paths {
		spec, err := loadSpec(path)
		if err != nil {
			return protocolSpec{}, err
		}

		if index == 0 {
			merged.Version = spec.Version
		}
		merged.Domains = append(merged.Domains, spec.Domains...)
	}

	return merged, nil
}

func domainNames(spec protocolSpec) []string {
	seen := make(map[string]struct{}, len(spec.Domains))
	names := make([]string, 0, len(spec.Domains))
	for _, domain := range spec.Domains {
		if domain.Domain == "" {
			continue
		}
		if _, ok := seen[domain.Domain]; ok {
			continue
		}

		seen[domain.Domain] = struct{}{}
		names = append(names, domain.Domain)
	}

	slices.Sort(names)

	return names
}

func findDomain(spec protocolSpec, name string) (domainSpec, error) {
	for _, domain := range spec.Domains {
		if domain.Domain == name {
			return domain, nil
		}
	}

	return domainSpec{}, fmt.Errorf("find domain %q: not found", name)
}

func lowerFirst(value string) string {
	if value == "" {
		return ""
	}

	runes := []rune(value)
	if len(runes) == 1 {
		return strings.ToLower(value)
	}

	prefix := 1
	for prefix < len(runes) {
		if !unicode.IsUpper(runes[prefix]) {
			break
		}
		if prefix+1 < len(runes) && unicode.IsLower(runes[prefix+1]) {
			break
		}
		prefix++
	}

	return avoidGoKeyword(strings.ToLower(string(runes[:prefix])) + string(runes[prefix:]))
}

func avoidGoKeyword(value string) string {
	switch value {
	case "break", "case", "chan", "const", "continue", "default", "defer", "else", "fallthrough",
		"for", "func", "go", "goto", "if", "import", "interface", "map", "package", "range",
		"return", "select", "struct", "switch", "type", "var":
		return value + "_"
	default:
		return value
	}
}
