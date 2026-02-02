package subagents

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/crush/plugin"
)

const (
	// ListDialogID is the identifier for the sub-agents list dialog.
	ListDialogID = "subagents-list"

	listDialogWidth  = 70
	listDialogHeight = 24
)

// ListDialog shows all available sub-agents.
type ListDialog struct {
	registry *Registry
	agents   []*SubAgent
	cursor   int
	width    int
	height   int
}

// NewListDialog creates a new sub-agents list dialog.
func NewListDialog(app *plugin.App) (plugin.PluginDialog, error) {
	registry := getRegistry()
	if registry == nil {
		return nil, fmt.Errorf("subagents registry not initialized")
	}

	agents := registry.List()
	// Sort by name for consistent ordering.
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].Name < agents[j].Name
	})

	return &ListDialog{
		registry: registry,
		agents:   agents,
		cursor:   0,
		width:    listDialogWidth,
		height:   listDialogHeight,
	}, nil
}

func (d *ListDialog) ID() string {
	return ListDialogID
}

func (d *ListDialog) Title() string {
	return "SubAgents"
}

func (d *ListDialog) Init() error {
	return nil
}

func (d *ListDialog) Update(event plugin.DialogEvent) (done bool, action plugin.PluginAction, err error) {
	switch e := event.(type) {
	case plugin.KeyEvent:
		switch e.Key {
		case "up", "k":
			if d.cursor > 0 {
				d.cursor--
			}
		case "down", "j":
			if d.cursor < len(d.agents)-1 {
				d.cursor++
			}
		case "enter":
			if len(d.agents) > 0 && d.cursor < len(d.agents) {
				// Set selected agent and open details dialog.
				SetSelectedAgent(d.agents[d.cursor].Name)
				return false, plugin.OpenDialogAction{DialogID: DetailsDialogID}, nil
			}
		case " ", "space":
			if len(d.agents) > 0 {
				d.toggleCurrent()
			}
		case "r":
			d.reloadAll()
		case "esc", "q":
			return true, plugin.NoAction{}, nil
		}
	case plugin.ResizeEvent:
		d.width = min(listDialogWidth, e.Width-10)
		d.height = min(listDialogHeight, e.Height-6)
	}
	return false, plugin.NoAction{}, nil
}

func (d *ListDialog) toggleCurrent() {
	if d.cursor < len(d.agents) {
		agent := d.agents[d.cursor]
		d.registry.SetEnabled(agent.Name, !agent.Enabled)
	}
}

func (d *ListDialog) reloadAll() {
	d.registry.ReloadAll()
	d.agents = d.registry.List()
	sort.Slice(d.agents, func(i, j int) bool {
		return d.agents[i].Name < d.agents[j].Name
	})
	if d.cursor >= len(d.agents) {
		d.cursor = max(0, len(d.agents)-1)
	}
}

func (d *ListDialog) View() string {
	var sb strings.Builder

	sb.WriteString("Manage custom sub-agents\n\n")

	if len(d.agents) == 0 {
		sb.WriteString("  No sub-agents found.\n\n")
		sb.WriteString("  Create agent files (.md) in:\n")
		for _, dir := range d.registry.cfg.Dirs {
			sb.WriteString(fmt.Sprintf("    - %s\n", dir))
		}
	} else {
		// Calculate column widths.
		maxNameLen := 20
		maxDirLen := d.width - maxNameLen - 12 // checkbox, spacing, etc.

		for i, agent := range d.agents {
			name := agent.Name
			if len(name) > maxNameLen {
				name = name[:maxNameLen-3] + "..."
			}

			// Show directory, truncated if needed.
			dir := shortenPath(agent.FilePath)
			if len(dir) > maxDirLen {
				dir = "..." + dir[len(dir)-maxDirLen+3:]
			}

			cursor := "  "
			if i == d.cursor {
				cursor = "> "
			}

			checkboxDisplay := "[ ]"
			if agent.Enabled {
				checkboxDisplay = "[x]"
			}

			line := fmt.Sprintf("%s%s %-*s  %s", cursor, checkboxDisplay, maxNameLen, name, dir)
			sb.WriteString(line + "\n")
		}
	}

	// Footer with help.
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("─", d.width-4) + "\n")
	sb.WriteString("↑/↓: Navigate  Enter: Details  Space: Toggle  r: Reload  Esc: Close")

	return sb.String()
}

func (d *ListDialog) Size() (width, height int) {
	contentHeight := 5 + len(d.agents) // Header + agents + footer
	if len(d.agents) == 0 {
		contentHeight = 10 // Space for "no agents" message
	}
	return d.width, min(contentHeight, d.height)
}

// GetSelectedAgent returns the currently selected agent name.
// Used by the details dialog to know which agent to show.
func (d *ListDialog) GetSelectedAgent() string {
	if d.cursor < len(d.agents) {
		return d.agents[d.cursor].Name
	}
	return ""
}

// shortenPath replaces home directory with ~ for display.
func shortenPath(path string) string {
	home, err := userHomeDir()
	if err != nil {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

var cachedHomeDir string

func userHomeDir() (string, error) {
	if cachedHomeDir != "" {
		return cachedHomeDir, nil
	}
	var err error
	cachedHomeDir, err = os.UserHomeDir()
	return cachedHomeDir, err
}
