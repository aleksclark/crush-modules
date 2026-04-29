// Package a2a provides an Agent-to-Agent Protocol (A2A) v1.0 plugin for Crush.
// It exposes Crush as an A2A server and provides client tools for invoking remote A2A agents.
// This plugin uses the official github.com/a2aproject/a2a-go/v2 SDK.
package a2a

import (
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

const (
	PluginName       = "a2a"
	HookName         = "a2a-server"
	DefaultPort      = 8200
	DefaultAgentName = "crush"
)

// Config is the client-side configuration for connecting to remote A2A servers.
// Configured in crush.json under options.plugins.a2a
type Config struct {
	Servers               []ServerConfig `json:"servers,omitempty"`
	DefaultTimeoutSeconds int            `json:"default_timeout_seconds,omitempty"`
}

// ServerConfig defines a single remote A2A server endpoint.
type ServerConfig struct {
	Name    string            `json:"name"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Enabled *bool             `json:"enabled,omitempty"`
}

func (s ServerConfig) IsEnabled() bool {
	if s.Enabled == nil {
		return true
	}
	return *s.Enabled
}

// A2AServerConfig is the server-side configuration for exposing Crush as an A2A agent.
// Configured in crush.json under options.plugins.a2a-server
type A2AServerConfig struct {
	Port        int           `json:"port,omitempty"`
	AgentName   string        `json:"agent_name,omitempty"`
	Description string        `json:"description,omitempty"`
	Skills      []SkillConfig `json:"skills,omitempty"`
}

// SkillConfig describes a skill to advertise in the agent card.
type SkillConfig struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Examples    []string `json:"examples,omitempty"`
}

func ExtractText(msg *a2a.Message) string {
	if msg == nil {
		return ""
	}
	return ExtractTextFromParts(msg.Parts)
}

func ExtractTextFromParts(parts a2a.ContentParts) string {
	var b strings.Builder
	for _, part := range parts {
		if text := part.Text(); text != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(text)
		}
	}
	return b.String()
}
