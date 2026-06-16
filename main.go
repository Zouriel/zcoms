package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"

	"zcoms/cmd"
)

func main() {
	if err := godotenv.Overload(); err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "failed to load .env:", err)
		os.Exit(1)
	}

	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		prependPathEntry(exeDir)
		if info, err := os.Stat(filepath.Join(exeDir, "bin")); err == nil && info.IsDir() {
			prependPathEntry(filepath.Join(exeDir, "bin"))
		}
	}

	if tdlibBin := strings.TrimSpace(os.Getenv("TDLIB_BIN")); tdlibBin != "" {
		absBin := tdlibBin
		if !filepath.IsAbs(tdlibBin) {
			if resolved, err := filepath.Abs(tdlibBin); err == nil {
				absBin = resolved
			}
		}
		prependPathEntry(absBin)
	}

	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func prependPathEntry(path string) {
	if path == "" {
		return
	}
	pathValue := os.Getenv("PATH")
	separator := string(os.PathListSeparator)
	if pathValue == "" {
		_ = os.Setenv("PATH", path)
		return
	}
	_ = os.Setenv("PATH", path+separator+pathValue)
}
