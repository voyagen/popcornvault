package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// loadEnvFiles sets environment variables from .env.local and .env.
// Looks in the current working directory and in the directory of the executable.
// Only sets variables that are not already set. Called when DATABASE_URL is missing.
func loadEnvFiles() {
	var dirs []string
	if cwd, err := os.Getwd(); err == nil {
		dirs = append(dirs, cwd)
	}
	if exe, err := os.Executable(); err == nil {
		if dir := filepath.Dir(exe); dir != "" {
			dirs = append(dirs, dir)
		}
	}
	for _, dir := range dirs {
		for _, name := range []string{".env.local", ".env"} {
			path := filepath.Join(dir, name)
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			applyEnvFile(data)
		}
	}
}

func applyEnvFile(data []byte) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, "=")
		if i <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:i])
		value := strings.TrimSpace(line[i+1:])
		value = strings.Trim(value, `"'`)
		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
}
