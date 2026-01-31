package periodicprompts

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/crush/plugin"
)

const (
	// DialogID is the identifier for the periodic prompts dialog.
	DialogID = "periodic-prompts-config"

	dialogWidth  = 60
	dialogHeight = 20
)

// Dialog implements a dialog for configuring periodic prompts.
type Dialog struct {
	hook          *Hook
	prompts       []PromptConfig
	enabledStates []bool // Track enabled state for each prompt
	allEnabled    bool   // Master toggle
	cursor        int    // Currently selected item (0 = all toggle, 1+ = individual prompts)
	width         int
	height        int
}

// NewDialog creates a new periodic prompts dialog.
func NewDialog(app *plugin.App) (plugin.PluginDialog, error) {
	hook := getHook()
	if hook == nil {
		return nil, fmt.Errorf("periodic-prompts hook not initialized")
	}

	prompts := hook.GetPrompts()
	enabledStates := make([]bool, len(prompts))

	// Initialize all as enabled if the master toggle is on.
	allEnabled := hook.IsEnabled()
	for i := range enabledStates {
		enabledStates[i] = allEnabled
	}

	return &Dialog{
		hook:          hook,
		prompts:       prompts,
		enabledStates: enabledStates,
		allEnabled:    allEnabled,
		cursor:        0,
		width:         dialogWidth,
		height:        dialogHeight,
	}, nil
}

func (d *Dialog) ID() string {
	return DialogID
}

func (d *Dialog) Title() string {
	return "Periodic Prompts"
}

func (d *Dialog) Init() error {
	return nil
}

func (d *Dialog) Update(event plugin.DialogEvent) (done bool, action plugin.PluginAction, err error) {
	switch e := event.(type) {
	case plugin.KeyEvent:
		switch e.Key {
		case "up", "k":
			if d.cursor > 0 {
				d.cursor--
			}
		case "down", "j":
			maxCursor := len(d.prompts) // 0 = all toggle, 1..n = prompts
			if d.cursor < maxCursor {
				d.cursor++
			}
		case "enter", " ", "space":
			d.toggleCurrent()
		case "esc":
			return true, plugin.NoAction{}, nil
		case "q":
			return true, plugin.NoAction{}, nil
		}
	case plugin.ResizeEvent:
		d.width = min(dialogWidth, e.Width-10)
		d.height = min(dialogHeight, e.Height-10)
	}
	return false, plugin.NoAction{}, nil
}

func (d *Dialog) toggleCurrent() {
	if d.cursor == 0 {
		// Toggle all.
		d.allEnabled = !d.allEnabled
		for i := range d.enabledStates {
			d.enabledStates[i] = d.allEnabled
		}
		d.hook.SetEnabled(d.allEnabled)
	} else {
		// Toggle individual prompt.
		idx := d.cursor - 1
		if idx < len(d.enabledStates) {
			d.enabledStates[idx] = !d.enabledStates[idx]
			// Update allEnabled based on whether any prompts are enabled.
			anyEnabled := false
			for _, enabled := range d.enabledStates {
				if enabled {
					anyEnabled = true
					break
				}
			}
			d.allEnabled = anyEnabled
			d.hook.SetEnabled(anyEnabled)
		}
	}
}

func (d *Dialog) View() string {
	var sb strings.Builder

	// Header with instructions.
	sb.WriteString("Toggle periodic prompts on/off.\n")
	sb.WriteString("Press Enter or Space to toggle.\n\n")

	// Master toggle.
	allCheckbox := "[ ]"
	if d.allEnabled {
		allCheckbox = "[x]"
	}
	allLine := fmt.Sprintf("%s Enable All Periodic Prompts", allCheckbox)
	if d.cursor == 0 {
		allLine = "> " + allLine
	} else {
		allLine = "  " + allLine
	}
	sb.WriteString(allLine + "\n")
	sb.WriteString(strings.Repeat("─", d.width-4) + "\n")

	// Individual prompts.
	if len(d.prompts) == 0 {
		sb.WriteString("\n  No prompts configured.\n")
		sb.WriteString("  Add prompts to crush.json under:\n")
		sb.WriteString("  options.plugins.periodic-prompts.prompts\n")
	} else {
		for i, p := range d.prompts {
			checkbox := "[ ]"
			if d.enabledStates[i] {
				checkbox = "[x]"
			}

			name := p.Name
			if name == "" {
				name = p.File
			}

			// Truncate long names.
			maxNameLen := d.width - 20
			if len(name) > maxNameLen {
				name = name[:maxNameLen-3] + "..."
			}

			line := fmt.Sprintf("%s %s", checkbox, name)
			if d.cursor == i+1 {
				line = "> " + line
			} else {
				line = "  " + line
			}
			sb.WriteString(line + "\n")

			// Show schedule on next line.
			schedule := fmt.Sprintf("     Schedule: %s", p.Schedule)
			sb.WriteString(schedule + "\n")
		}
	}

	// Footer with help.
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("─", d.width-4) + "\n")
	sb.WriteString("↑/↓: Navigate  Enter/Space: Toggle  Esc: Close")

	return sb.String()
}

func (d *Dialog) Size() (width, height int) {
	// Calculate height based on content.
	height = 8 + len(d.prompts)*2 // Base + 2 lines per prompt
	height = min(height, d.height)
	return d.width, height
}

func init() {
	// Register the dialog factory.
	plugin.RegisterDialog(DialogID, func(app *plugin.App) (plugin.PluginDialog, error) {
		return NewDialog(app)
	})

	// Register the command to open the dialog.
	plugin.RegisterCommand(
		plugin.PluginCommand{
			ID:          "periodic-prompts",
			Title:       "Periodic Prompts",
			Description: "Configure scheduled prompts",
		},
		func(cmd plugin.PluginCommand) plugin.PluginAction {
			return plugin.OpenDialogAction{DialogID: DialogID}
		},
	)
}
