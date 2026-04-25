package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"nncode/internal/agent"
	"nncode/internal/skills"
)

type activateSkillArgs struct {
	Name string `json:"name"`
}

// ActivateSkill returns a dynamic tool that loads full Agent Skill content on
// demand. It should only be registered when at least one skill is visible to
// the model.
func ActivateSkill(activator *skills.Activator) agent.Tool {
	var catalog skills.Catalog
	if activator != nil && activator.Registry() != nil {
		catalog = activator.Registry().ModelCatalog()
	}
	schema, err := activateSkillSchema(catalog.Names())
	if err != nil {
		schema = `{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}`
	}
	return agent.Tool{
		Name:        "activate_skill",
		Description: "Load the full instructions for an available Agent Skill by name before applying it.",
		Parameters:  schema,
		Execute: func(ctx context.Context, raw json.RawMessage) (agent.ToolResult, error) {
			var args activateSkillArgs
			if err := json.Unmarshal(raw, &args); err != nil {
				return agent.ToolResult{Content: fmt.Sprintf("Invalid arguments: %v", err), IsError: true}, nil
			}
			if activator == nil {
				return agent.ToolResult{Content: "skills are not configured", IsError: true}, nil
			}
			if !catalog.Contains(args.Name) {
				if registry := activator.Registry(); registry != nil {
					if skill, ok := registry.Lookup(args.Name); ok && !skill.DisableModelInvocation {
						return agent.ToolResult{Content: fmt.Sprintf("skill %q is not in the model activation catalog; use /skills to inspect available skills or /skill:%s to activate it manually", args.Name, args.Name), IsError: true}, nil
					}
				}
			}
			activation, err := activator.Activate(args.Name, false)
			if err != nil {
				return agent.ToolResult{Content: err.Error(), IsError: true}, nil
			}
			return agent.ToolResult{Content: skills.FormatActivation(activation)}, nil
		},
	}
}

func activateSkillSchema(names []string) (string, error) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Exact Agent Skill name to activate.",
				"enum":        names,
			},
		},
		"required":             []string{"name"},
		"additionalProperties": false,
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
