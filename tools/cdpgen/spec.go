package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
)

type protocolSpec struct {
	Version struct {
		Major string `json:"major"`
		Minor string `json:"minor"`
	} `json:"version"`
	Domains []domainSpec `json:"domains"`
}

type domainSpec struct {
	Domain string `json:"domain"`
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

func domainNames(spec protocolSpec) []string {
	names := make([]string, 0, len(spec.Domains))
	for _, domain := range spec.Domains {
		if domain.Domain == "" {
			continue
		}

		names = append(names, domain.Domain)
	}

	slices.Sort(names)

	return names
}
