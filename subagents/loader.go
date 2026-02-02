package subagents

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// SubAgent represents a loaded sub-agent configuration.
type SubAgent struct {
	Name            string   `yaml:"name"`
	Description     string   `yaml:"description"`
	Tools           []string `yaml:"-"`          // Parsed from comma-separated string
	ToolsRaw        string   `yaml:"tools"`      // Raw YAML field
	DisallowedTools []string `yaml:"-"`          // Parsed from comma-separated string
	DisallowedRaw   string   `yaml:"disallowedTools"`
	Model           string   `yaml:"model"`
	PermissionMode  string   `yaml:"permissionMode"`
	SystemPrompt    string   `yaml:"-"` // Markdown body
	FilePath        string   `yaml:"-"` // Source file path
	Enabled         bool     `yaml:"-"` // Runtime state
}

// LoadAgentFile parses a sub-agent YAML+Markdown file.
func LoadAgentFile(path string) (*SubAgent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	frontmatter, body, err := splitFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", err)
	}

	var agent SubAgent
	if err := yaml.Unmarshal(frontmatter, &agent); err != nil {
		return nil, fmt.Errorf("unmarshal yaml: %w", err)
	}

	if agent.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if agent.Description == "" {
		return nil, fmt.Errorf("description is required")
	}

	// Parse comma-separated tool lists.
	agent.Tools = parseToolList(agent.ToolsRaw)
	agent.DisallowedTools = parseToolList(agent.DisallowedRaw)
	agent.SystemPrompt = strings.TrimSpace(string(body))
	agent.FilePath = path
	agent.Enabled = true

	// Default model to inherit.
	if agent.Model == "" {
		agent.Model = "inherit"
	}

	return &agent, nil
}

// splitFrontmatter separates YAML frontmatter from markdown body.
// Expects format:
// ---
// yaml content
// ---
// markdown body
func splitFrontmatter(data []byte) (frontmatter, body []byte, err error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))

	// Find opening ---.
	if !scanner.Scan() {
		return nil, nil, fmt.Errorf("empty file")
	}
	if strings.TrimSpace(scanner.Text()) != "---" {
		return nil, nil, fmt.Errorf("file must start with ---")
	}

	// Read frontmatter until closing ---.
	var fmLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		fmLines = append(fmLines, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}

	frontmatter = []byte(strings.Join(fmLines, "\n"))

	// Rest is markdown body.
	var bodyLines []string
	for scanner.Scan() {
		bodyLines = append(bodyLines, scanner.Text())
	}
	body = []byte(strings.Join(bodyLines, "\n"))

	return frontmatter, body, nil
}

// parseToolList splits a comma-separated tool list into individual tool names.
func parseToolList(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	tools := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t != "" {
			tools = append(tools, t)
		}
	}
	return tools
}

// ExpandPath expands ~ to home directory and resolves relative paths.
func ExpandPath(path, workingDir string) string {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[1:])
		}
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(workingDir, path)
	}
	return filepath.Clean(path)
}

// DiscoverAgentFiles finds all .md files in the given directories.
func DiscoverAgentFiles(dirs []string, workingDir string) []string {
	var files []string
	seen := make(map[string]bool)

	for _, dir := range dirs {
		expanded := ExpandPath(dir, workingDir)
		entries, err := os.ReadDir(expanded)
		if err != nil {
			continue // Skip non-existent directories.
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			path := filepath.Join(expanded, entry.Name())
			if seen[path] {
				continue
			}
			seen[path] = true
			files = append(files, path)
		}
	}

	return files
}
