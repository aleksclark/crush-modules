package subagents

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadAgentFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		content     string
		wantAgent   *SubAgent
		wantErr     bool
		errContains string
	}{
		{
			name: "valid agent with all fields",
			content: `---
name: code-reviewer
description: Expert code reviewer for quality checks
tools: Read, Grep, Glob
model: sonnet
permissionMode: acceptEdits
---

You are a senior code reviewer with expertise in Go and TypeScript.
Review code changes carefully.`,
			wantAgent: &SubAgent{
				Name:           "code-reviewer",
				Description:    "Expert code reviewer for quality checks",
				Tools:          []string{"Read", "Grep", "Glob"},
				Model:          "sonnet",
				PermissionMode: "acceptEdits",
				SystemPrompt:   "You are a senior code reviewer with expertise in Go and TypeScript.\nReview code changes carefully.",
				Enabled:        true,
			},
		},
		{
			name: "minimal agent",
			content: `---
name: helper
description: A helpful assistant
---

Be helpful.`,
			wantAgent: &SubAgent{
				Name:         "helper",
				Description:  "A helpful assistant",
				Model:        "inherit",
				SystemPrompt: "Be helpful.",
				Enabled:      true,
			},
		},
		{
			name: "agent with disallowed tools",
			content: `---
name: safe-agent
description: Agent with restricted tools
disallowedTools: Bash, Write
---

You cannot use Bash or Write tools.`,
			wantAgent: &SubAgent{
				Name:            "safe-agent",
				Description:     "Agent with restricted tools",
				DisallowedTools: []string{"Bash", "Write"},
				Model:           "inherit",
				SystemPrompt:    "You cannot use Bash or Write tools.",
				Enabled:         true,
			},
		},
		{
			name: "missing name",
			content: `---
description: No name
---

Body.`,
			wantErr:     true,
			errContains: "name is required",
		},
		{
			name: "missing description",
			content: `---
name: test
---

Body.`,
			wantErr:     true,
			errContains: "description is required",
		},
		{
			name:        "empty file",
			content:     "",
			wantErr:     true,
			errContains: "empty file",
		},
		{
			name:        "no frontmatter",
			content:     "Just some text",
			wantErr:     true,
			errContains: "must start with ---",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create temp file.
			tmpDir := t.TempDir()
			path := filepath.Join(tmpDir, "agent.md")
			err := os.WriteFile(path, []byte(tt.content), 0o644)
			require.NoError(t, err)

			agent, err := LoadAgentFile(path)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					require.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, agent)

			require.Equal(t, tt.wantAgent.Name, agent.Name)
			require.Equal(t, tt.wantAgent.Description, agent.Description)
			require.Equal(t, tt.wantAgent.Tools, agent.Tools)
			require.Equal(t, tt.wantAgent.DisallowedTools, agent.DisallowedTools)
			require.Equal(t, tt.wantAgent.Model, agent.Model)
			require.Equal(t, tt.wantAgent.PermissionMode, agent.PermissionMode)
			require.Equal(t, tt.wantAgent.SystemPrompt, agent.SystemPrompt)
			require.Equal(t, tt.wantAgent.Enabled, agent.Enabled)
			require.Equal(t, path, agent.FilePath)
		})
	}
}

func TestParseToolList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected []string
	}{
		{"", nil},
		{"Read", []string{"Read"}},
		{"Read, Grep, Glob", []string{"Read", "Grep", "Glob"}},
		{"Read,Grep,Glob", []string{"Read", "Grep", "Glob"}},
		{"  Read  ,  Grep  ", []string{"Read", "Grep"}},
		{"Read,,Grep", []string{"Read", "Grep"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			result := parseToolList(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestExpandPath(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	tests := []struct {
		name       string
		path       string
		workingDir string
		expected   string
	}{
		{
			name:       "tilde expansion",
			path:       "~/agents",
			workingDir: "/tmp",
			expected:   filepath.Join(home, "agents"),
		},
		{
			name:       "relative path",
			path:       ".crush/agents",
			workingDir: "/home/user/project",
			expected:   "/home/user/project/.crush/agents",
		},
		{
			name:       "absolute path unchanged",
			path:       "/etc/agents",
			workingDir: "/tmp",
			expected:   "/etc/agents",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ExpandPath(tt.path, tt.workingDir)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestDiscoverAgentFiles(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// Create directory structure.
	dir1 := filepath.Join(tmpDir, "dir1")
	dir2 := filepath.Join(tmpDir, "dir2")
	require.NoError(t, os.MkdirAll(dir1, 0o755))
	require.NoError(t, os.MkdirAll(dir2, 0o755))

	// Create agent files.
	require.NoError(t, os.WriteFile(filepath.Join(dir1, "agent1.md"), []byte("test"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir1, "agent2.md"), []byte("test"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir2, "agent3.md"), []byte("test"), 0o644))

	// Create non-md files (should be ignored).
	require.NoError(t, os.WriteFile(filepath.Join(dir1, "readme.txt"), []byte("test"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir1, "config.yaml"), []byte("test"), 0o644))

	files := DiscoverAgentFiles([]string{dir1, dir2}, tmpDir)

	require.Len(t, files, 3)
	// All should be .md files.
	for _, f := range files {
		require.True(t, filepath.Ext(f) == ".md")
	}
}

func TestDiscoverAgentFilesNonExistentDir(t *testing.T) {
	t.Parallel()

	files := DiscoverAgentFiles([]string{"/nonexistent/path"}, "/tmp")
	require.Empty(t, files)
}

func TestSplitFrontmatter(t *testing.T) {
	t.Parallel()

	content := `---
name: test
description: test desc
---

This is the body.
Multiple lines.`

	fm, body, err := splitFrontmatter([]byte(content))
	require.NoError(t, err)
	require.Contains(t, string(fm), "name: test")
	require.Contains(t, string(body), "This is the body")
}
