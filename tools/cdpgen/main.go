package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	inputPath := flag.String("input", "", "path to a CDP protocol JSON file")
	printDomains := flag.Bool("print-domains", false, "print parsed domain names")
	flag.Parse()

	if *inputPath == "" {
		fmt.Fprintln(os.Stderr, "cdpgen: -input is required")
		os.Exit(2)
	}

	spec, err := loadSpec(*inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cdpgen: %v\n", err)
		os.Exit(1)
	}

	if *printDomains {
		for _, name := range domainNames(spec) {
			fmt.Println(name)
		}
	}
}
