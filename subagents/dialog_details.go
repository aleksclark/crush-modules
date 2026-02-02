package subagents

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/crush/plugin"
)

const (
	// DetailsDialogID is the identifier for the sub-agent details dialog.
	DetailsDialogID = "subagents-details"

	detailsDialogWidth  = 70
	detailsDialogHeight = 20
)

// selectedAgentName is set by the list dialog before opening details.
var selectedAgentName string

// SetSelectedAgent sets the agent to show in the details dialog.
func SetSelectedAgent(name string) {
	selectedAgentName = name
}

// DetailsDialog shows details for a specific sub-agent.
type DetailsDialog struct {
	registry    *Registry
	agent       *SubAgent
	cursor      int // 0=View Prompt, 1=Toggle, 2=Reload, 3=Close
	showPrompt  bool
	promptScroll int
	width       int
	height      int
}

// NewDetailsDialog creates a new sub-agent details dialog.
func NewDetailsDialog(app *plugin.App) (plugin.PluginDialog, error) {
	registry := getRegistry()
	if registry == nil {
		return nil, fmt.Errorf("subagents registry not initialized")
	}

	name := selectedAgentName
	if name == "" {
		return nil, fmt.Errorf("no agent selected")
	}

	agent, ok := registry.Get(name)
	if !ok {
		return nil, fmt.Errorf("agent not found: %s", name)
	}

	return &DetailsDialog{
		registry: registry,
		agent:    agent,
		cursor:   0,
		width:    detailsDialogWidth,
		height:   detailsDialogHeight,
	}, nil
}

func (d *DetailsDialog) ID() string {
	return DetailsDialogID
}

func (d *DetailsDialog) Title() string {
	return d.agent.Name
}

func (d *DetailsDialog) Init() error {
	return nil
}

func (d *DetailsDialog) Update(event plugin.DialogEvent) (done bool, action plugin.PluginAction, err error) {
	switch e := event.(type) {
	case plugin.KeyEvent:
		if d.showPrompt {
			return d.updatePromptView(e.Key)
		}
		return d.updateMainView(e.Key)
	case plugin.ResizeEvent:
		d.width = min(detailsDialogWidth, e.Width-10)
		d.height = min(detailsDialogHeight, e.Height-6)
	}
	return false, plugin.NoAction{}, nil
}

func (d *DetailsDialog) updateMainView(key string) (bool, plugin.PluginAction, error) {
	switch key {
	case "left", "h":
		if d.cursor > 0 {
			d.cursor--
		}
	case "right", "l":
		if d.cursor < 3 {
			d.cursor++
		}
	case "enter", " ", "space":
		return d.handleAction()
	case "esc", "q":
		return true, plugin.NoAction{}, nil
	case "v":
		d.showPrompt = true
		d.promptScroll = 0
	case "t":
		d.toggleAgent()
	case "r":
		d.reloadAgent()
	}
	return false, plugin.NoAction{}, nil
}

func (d *DetailsDialog) updatePromptView(key string) (bool, plugin.PluginAction, error) {
	switch key {
	case "esc", "q":
		d.showPrompt = false
	case "up", "k":
		if d.promptScroll > 0 {
			d.promptScroll--
		}
	case "down", "j":
		d.promptScroll++
	}
	return false, plugin.NoAction{}, nil
}

func (d *DetailsDialog) handleAction() (bool, plugin.PluginAction, error) {
	switch d.cursor {
	case 0: // View Prompt
		d.showPrompt = true
		d.promptScroll = 0
	case 1: // Toggle
		d.toggleAgent()
	case 2: // Reload
		d.reloadAgent()
	case 3: // Close
		return true, plugin.NoAction{}, nil
	}
	return false, plugin.NoAction{}, nil
}

func (d *DetailsDialog) toggleAgent() {
	d.registry.SetEnabled(d.agent.Name, !d.agent.Enabled)
}

func (d *DetailsDialog) reloadAgent() {
	if err := d.registry.ReloadAgent(d.agent.Name); err == nil {
		// Refresh our reference.
		if agent, ok := d.registry.Get(d.agent.Name); ok {
			d.agent = agent
		}
	}
}

func (d *DetailsDialog) View() string {
	if d.showPrompt {
		return d.viewPrompt()
	}
	return d.viewDetails()
}

func (d *DetailsDialog) viewDetails() string {
	var sb strings.Builder

	// Description.
	desc := d.agent.Description
	if len(desc) > d.width-4 {
		desc = desc[:d.width-7] + "..."
	}
	sb.WriteString(desc + "\n\n")

	// File path.
	sb.WriteString(fmt.Sprintf("File: %s\n", shortenPath(d.agent.FilePath)))

	// Model.
	sb.WriteString(fmt.Sprintf("Model: %s\n", d.agent.Model))

	// Tools.
	tools := "inherit all"
	if len(d.agent.Tools) > 0 {
		tools = strings.Join(d.agent.Tools, ", ")
	}
	if len(d.agent.DisallowedTools) > 0 {
		tools += fmt.Sprintf(" (except: %s)", strings.Join(d.agent.DisallowedTools, ", "))
	}
	sb.WriteString(fmt.Sprintf("Tools: %s\n", tools))

	// Permission mode.
	if d.agent.PermissionMode != "" {
		sb.WriteString(fmt.Sprintf("Permission Mode: %s\n", d.agent.PermissionMode))
	}

	// Status.
	status := "Disabled"
	if d.agent.Enabled {
		status = "Enabled"
	}
	sb.WriteString(fmt.Sprintf("Status: [%s] %s\n", statusChar(d.agent.Enabled), status))

	// Action buttons.
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("─", d.width-4) + "\n")

	buttons := []string{"View Prompt", "Toggle", "Reload", "Close"}
	var btnLine strings.Builder
	for i, btn := range buttons {
		if i == d.cursor {
			btnLine.WriteString(fmt.Sprintf("[%s]  ", btn))
		} else {
			btnLine.WriteString(fmt.Sprintf(" %s   ", btn))
		}
	}
	sb.WriteString(btnLine.String() + "\n")
	sb.WriteString("←/→: Select  Enter: Action  v: View  t: Toggle  r: Reload  Esc: Back")

	return sb.String()
}

func (d *DetailsDialog) viewPrompt() string {
	var sb strings.Builder

	sb.WriteString("System Prompt (↑/↓ to scroll, Esc to close)\n")
	sb.WriteString(strings.Repeat("─", d.width-4) + "\n\n")

	lines := strings.Split(d.agent.SystemPrompt, "\n")
	maxLines := d.height - 6

	// Apply scroll offset.
	startLine := d.promptScroll
	if startLine > len(lines)-maxLines {
		startLine = max(0, len(lines)-maxLines)
		d.promptScroll = startLine
	}

	endLine := min(startLine+maxLines, len(lines))
	for i := startLine; i < endLine; i++ {
		line := lines[i]
		if len(line) > d.width-4 {
			line = line[:d.width-7] + "..."
		}
		sb.WriteString(line + "\n")
	}

	// Scroll indicator.
	if len(lines) > maxLines {
		sb.WriteString(fmt.Sprintf("\n[%d-%d of %d lines]", startLine+1, endLine, len(lines)))
	}

	return sb.String()
}

func (d *DetailsDialog) Size() (width, height int) {
	return d.width, d.height
}

func statusChar(enabled bool) string {
	if enabled {
		return "x"
	}
	return " "
}

func init() {
	// Register dialogs.
	plugin.RegisterDialog(ListDialogID, func(app *plugin.App) (plugin.PluginDialog, error) {
		return NewListDialog(app)
	})

	plugin.RegisterDialog(DetailsDialogID, func(app *plugin.App) (plugin.PluginDialog, error) {
		return NewDetailsDialog(app)
	})

	// Register the command to open the list dialog.
	plugin.RegisterCommand(
		plugin.PluginCommand{
			ID:          "subagents",
			Title:       "SubAgents",
			Description: "Manage custom sub-agents",
		},
		func(cmd plugin.PluginCommand) plugin.PluginAction {
			return plugin.OpenDialogAction{DialogID: ListDialogID}
		},
	)
}
