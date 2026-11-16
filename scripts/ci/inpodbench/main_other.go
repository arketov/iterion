//go:build !linux

// Command inpodbench is a Linux-only container probe (see main.go). This stub
// keeps the package buildable and vet-able by explicit path on other hosts.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "inpodbench: Linux only")
	os.Exit(2)
}
