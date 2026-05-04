package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	inputPaths := flag.String("input", "", "comma-separated path list for CDP protocol JSON files")
	printDomains := flag.Bool("print-domains", false, "print parsed domain names")
	renderDomainName := flag.String("render-domain", "", "render a single domain package to stdout")
	generateDomains := flag.String("generate-domains", "", "comma-separated domain list to generate")
	outputDir := flag.String("out", "", "output directory for generated domain packages")
	flag.Parse()

	if *inputPaths == "" {
		fmt.Fprintln(os.Stderr, "cdpgen: -input is required")
		os.Exit(2)
	}

	spec, err := loadSpecs(splitCSV(*inputPaths))
	if err != nil {
		fmt.Fprintf(os.Stderr, "cdpgen: %v\n", err)
		os.Exit(1)
	}

	if *printDomains {
		for _, name := range domainNames(spec) {
			fmt.Println(name)
		}
	}

	if *renderDomainName != "" {
		domain, err := findDomain(spec, *renderDomainName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cdpgen: %v\n", err)
			os.Exit(1)
		}

		rendered, err := renderDomainWithTypeIndex(domain, buildTypeIndex(spec))
		if err != nil {
			fmt.Fprintf(os.Stderr, "cdpgen: %v\n", err)
			os.Exit(1)
		}

		_, _ = os.Stdout.Write(rendered)
	}

	if *generateDomains != "" {
		if *outputDir == "" {
			fmt.Fprintln(os.Stderr, "cdpgen: -out is required with -generate-domains")
			os.Exit(2)
		}

		for _, name := range splitCSV(*generateDomains) {
			domain, err := findDomain(spec, name)
			if err != nil {
				fmt.Fprintf(os.Stderr, "cdpgen: %v\n", err)
				os.Exit(1)
			}

			if err := writeDomainPackage(*outputDir, domain, buildTypeIndex(spec)); err != nil {
				fmt.Fprintf(os.Stderr, "cdpgen: %v\n", err)
				os.Exit(1)
			}
		}
	}
}

func splitCSV(value string) []string {
	raw := strings.Split(value, ",")
	items := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		items = append(items, item)
	}

	return items
}

func writeDomainPackage(outputDir string, domain domainSpec, typeIndex map[string]typeSpec) error {
	rendered, err := renderDomainWithTypeIndex(domain, typeIndex)
	if err != nil {
		return err
	}

	packageDir := filepath.Join(outputDir, packageNameForDomain(domain.Domain))
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		return fmt.Errorf("create package dir %q: %w", packageDir, err)
	}

	outputPath := filepath.Join(packageDir, packageNameForDomain(domain.Domain)+".go")
	if err := os.WriteFile(outputPath, rendered, 0o644); err != nil {
		return fmt.Errorf("write generated package %q: %w", outputPath, err)
	}

	return nil
}
