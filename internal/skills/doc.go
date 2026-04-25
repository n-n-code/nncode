// Package skills implements nncode's local Agent Skills support.
//
// Discovery is intentionally shallow: startup reads only SKILL.md frontmatter
// from .agents/skills directories and records names, descriptions, visibility,
// and diagnostics. Full skill instructions and resource listings are loaded
// later through Activator so the base prompt stays small. The model-visible
// catalog is bounded and reused by both the system prompt and activate_skill
// tool schema; omitted skills can still be activated manually by the CLI.
package skills
