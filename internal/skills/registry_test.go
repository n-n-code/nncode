package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeSkill(t *testing.T, root string, name string, frontmatter string, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0755))
	content := "---\n" + frontmatter + "---\n" + body
	path := filepath.Join(dir, "SKILL.md")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return dir
}

func TestDiscoverProjectAncestorPrecedence(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	cwd := filepath.Join(root, "a", "b")
	require.NoError(t, os.MkdirAll(cwd, 0755))

	rootSkills := filepath.Join(root, ".agents", "skills")
	childSkills := filepath.Join(cwd, ".agents", "skills")
	writeSkill(t, rootSkills, "go", "name: go\ndescription: root go\n", "# root")
	childDir := writeSkill(t, childSkills, "go-local", "name: go\ndescription: child go\n", "# child")
	writeSkill(t, rootSkills, "docs", "name: docs\ndescription: docs\n", "# docs")

	reg := Discover(DiscoverOptions{CWD: cwd, HomeDir: t.TempDir()})

	skill, ok := reg.Lookup("go")
	require.True(t, ok)
	assert.Equal(t, childDir, skill.Dir)
	assert.Equal(t, "child go", skill.Description)
	_, ok = reg.Lookup("docs")
	assert.True(t, ok)
	assertContainsDiagnostic(t, reg.Diagnostics(), "already defines it")
}

func TestDiscoverGlobalLowerPrecedence(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	home := t.TempDir()

	projectDir := writeSkill(t, filepath.Join(root, ".agents", "skills"), "go", "name: go\ndescription: project\n", "# project")
	writeSkill(t, filepath.Join(home, ".agents", "skills"), "go-global", "name: go\ndescription: global\n", "# global")
	writeSkill(t, filepath.Join(home, ".agents", "skills"), "global-only", "name: global-only\ndescription: global only\n", "# global")

	reg := Discover(DiscoverOptions{CWD: root, HomeDir: home})

	goSkill, ok := reg.Lookup("go")
	require.True(t, ok)
	assert.Equal(t, projectDir, goSkill.Dir)
	global, ok := reg.Lookup("global-only")
	require.True(t, ok)
	assert.Equal(t, "global", global.Source)
}

func TestDiscoverIgnoresOtherSkillLocations(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	writeSkill(t, filepath.Join(root, ".nncode", "skills"), "nncode", "name: nncode\ndescription: ignored\n", "# ignored")
	writeSkill(t, filepath.Join(root, ".claude", "skills"), "claude", "name: claude\ndescription: ignored\n", "# ignored")
	writeSkill(t, filepath.Join(root, ".codex", "skills"), "codex", "name: codex\ndescription: ignored\n", "# ignored")

	reg := Discover(DiscoverOptions{CWD: root, HomeDir: t.TempDir()})

	assert.Empty(t, reg.Skills())
}

func TestDiscoverSkipsMissingDescriptionAndBadFrontmatter(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	skillsDir := filepath.Join(root, ".agents", "skills")
	writeSkill(t, skillsDir, "missing-description", "name: missing-description\n", "# body")
	badDir := filepath.Join(skillsDir, "bad")
	require.NoError(t, os.MkdirAll(badDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(badDir, "SKILL.md"), []byte("---\nname: bad\n"), 0644))

	reg := Discover(DiscoverOptions{CWD: root, HomeDir: t.TempDir()})

	assert.Empty(t, reg.Skills())
	assertContainsDiagnostic(t, reg.Diagnostics(), "missing required frontmatter field: description")
	assertContainsDiagnostic(t, reg.Diagnostics(), "missing closing frontmatter marker")
}

func TestDiscoverHiddenSkillAndCosmeticWarning(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	writeSkill(t, filepath.Join(root, ".agents", "skills"), "folder-name", "name: hidden-skill\ndescription: hidden\ndisable-model-invocation: true\n", "# hidden")

	reg := Discover(DiscoverOptions{CWD: root, HomeDir: t.TempDir()})

	all := reg.Skills()
	require.Len(t, all, 1)
	assert.True(t, all[0].DisableModelInvocation)
	assert.Empty(t, reg.ModelVisibleSkills())
	assertContainsDiagnostic(t, reg.Diagnostics(), "differs from skill name")
}

func TestParseFrontmatterSupportsQuotedAndFoldedValues(t *testing.T) {
	fm, err := parseFrontmatter("name: \"go\"\ndescription: >-\n  first line\n  second line\ndisable-model-invocation: true\n")

	require.NoError(t, err)
	assert.Equal(t, "go", fm.Name)
	assert.Equal(t, "first line second line", normalizeWhitespace(fm.Description))
	assert.True(t, fm.DisableModelInvocation)
}

func TestParseFrontmatterWarnsOnUnsupportedFieldsAndNestedValues(t *testing.T) {
	fm, err := parseFrontmatter("name: go\ndescription: go skill\ntags:\n  - go\n")

	require.NoError(t, err)
	assert.Equal(t, "go", fm.Name)
	assert.Equal(t, "go skill", fm.Description)
	assertContainsDiagnostic(t, fm.Diagnostics, "unsupported nested value")
	assertContainsDiagnostic(t, fm.Diagnostics, "unsupported frontmatter field")
}

func TestActivatorLoadsBodyResourcesAndDeduplicates(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	dir := writeSkill(t, filepath.Join(root, ".agents", "skills"), "go", "name: go\ndescription: go skill\n", "# Go\n\nUse Go.")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "references"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "references", "testing.md"), []byte("details"), 0644))

	reg := Discover(DiscoverOptions{CWD: root, HomeDir: t.TempDir()})
	activator := NewActivator(reg)

	activation, err := activator.Activate("go", false)
	require.NoError(t, err)
	assert.Equal(t, "# Go\n\nUse Go.", activation.Content)
	assert.Equal(t, []string{"references/testing.md"}, activation.Resources)
	formatted := FormatActivation(activation)
	assert.Contains(t, formatted, activationMarkerOpen)
	assert.Contains(t, formatted, "<skill_content>")

	again, err := activator.Activate("go", false)
	require.NoError(t, err)
	assert.True(t, again.Duplicate)
	assert.NotContains(t, FormatActivation(again), "<skill_content>")
}

func TestActivatorCanRehydrateDeduplicationFromText(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	writeSkill(t, filepath.Join(root, ".agents", "skills"), "go", "name: go\ndescription: go skill\n", "# Go")
	reg := Discover(DiscoverOptions{CWD: root, HomeDir: t.TempDir()})
	activator := NewActivator(reg)

	activator.MarkActivatedFromText(`<activated_skill>{"name":"go"}</activated_skill>`)
	activation, err := activator.Activate("go", false)

	require.NoError(t, err)
	assert.True(t, activation.Duplicate)
}

func TestActivatorCanRehydrateLegacyActivationText(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	writeSkill(t, filepath.Join(root, ".agents", "skills"), "go", "name: go\ndescription: go skill\n", "# Go")
	reg := Discover(DiscoverOptions{CWD: root, HomeDir: t.TempDir()})
	activator := NewActivator(reg)

	activator.MarkActivatedFromText("Skill \"go\" activated.\n<skill_content>\n# Go\n</skill_content>")
	activation, err := activator.Activate("go", false)

	require.NoError(t, err)
	assert.True(t, activation.Duplicate)
}

func TestActivatorCapsResourceListing(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	dir := writeSkill(t, filepath.Join(root, ".agents", "skills"), "go", "name: go\ndescription: go skill\n", "# Go")
	for i := 0; i < defaultResourceCap+2; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("resource-%02d.md", i)), []byte("x"), 0644))
	}
	reg := Discover(DiscoverOptions{CWD: root, HomeDir: t.TempDir()})
	activator := NewActivator(reg)

	activation, err := activator.Activate("go", false)

	require.NoError(t, err)
	assert.Len(t, activation.Resources, defaultResourceCap)
	assert.Equal(t, 2, activation.ResourcesTruncated)
}

func TestActivatorRejectsHiddenForModelAllowsExplicit(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	writeSkill(t, filepath.Join(root, ".agents", "skills"), "manual", "name: manual\ndescription: manual\ndisable-model-invocation: true\n", "# Manual")
	reg := Discover(DiscoverOptions{CWD: root, HomeDir: t.TempDir()})
	activator := NewActivator(reg)

	_, err := activator.Activate("manual", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "manual-only")

	activation, err := activator.Activate("manual", true)
	require.NoError(t, err)
	assert.Equal(t, "# Manual", activation.Content)
}

func TestComposeSystemPromptIncludesVisibleCatalogOnly(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	skillsDir := filepath.Join(root, ".agents", "skills")
	writeSkill(t, skillsDir, "go", "name: go\ndescription: go skill\n", "# Go")
	writeSkill(t, skillsDir, "manual", "name: manual\ndescription: manual\ndisable-model-invocation: true\n", "# Manual")
	reg := Discover(DiscoverOptions{CWD: root, HomeDir: t.TempDir()})

	prompt := ComposeSystemPrompt("base", reg)

	assert.Contains(t, prompt, "base")
	assert.Contains(t, prompt, "- go: go skill")
	assert.NotContains(t, prompt, "manual")
	assert.NotContains(t, prompt, "# Go")
}

func TestComposeSystemPromptCapsCatalogAndAddsDiagnostic(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	skillsDir := filepath.Join(root, ".agents", "skills")
	for i := 0; i < maxCatalogSkills+2; i++ {
		name := fmt.Sprintf("skill-%02d", i)
		writeSkill(t, skillsDir, name, fmt.Sprintf("name: %s\ndescription: skill %d\n", name, i), "# Skill")
	}
	reg := Discover(DiscoverOptions{CWD: root, HomeDir: t.TempDir()})

	prompt := ComposeSystemPrompt("base", reg)

	assert.Contains(t, prompt, fmt.Sprintf("%d more skills omitted", 2))
	assertContainsDiagnostic(t, reg.Diagnostics(), "skill catalog truncated")
}

func TestModelCatalogCapsNamesAndDiagnosticsWithoutPromptComposition(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	skillsDir := filepath.Join(root, ".agents", "skills")
	for i := 0; i < maxCatalogSkills+1; i++ {
		name := fmt.Sprintf("skill-%02d", i)
		writeSkill(t, skillsDir, name, fmt.Sprintf("name: %s\ndescription: skill %d\n", name, i), "# Skill")
	}
	reg := Discover(DiscoverOptions{CWD: root, HomeDir: t.TempDir()})

	catalog := reg.ModelCatalog()

	require.Len(t, catalog.Skills, maxCatalogSkills)
	assert.Len(t, catalog.Names(), maxCatalogSkills)
	assert.Equal(t, 1, catalog.Omitted)
	assert.False(t, catalog.Contains("skill-64"))
	assertContainsDiagnostic(t, reg.Diagnostics(), "activate_skill enum")
}

func TestStripActivationMarkersForDisplay(t *testing.T) {
	text := `<activated_skill>{"name":"go"}</activated_skill> Skill "go" activated.`

	stripped := StripActivationMarkers(text)

	assert.Equal(t, `Skill "go" activated.`, stripped)
}

func TestReadSkillBodyRequiresExactClosingMarkerLine(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(root, 0755))
	path := filepath.Join(root, "SKILL.md")
	content := "---\nname: go\ndescription: go\n---\n# Go\n\n--- not a closing marker\nbody"
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	body, err := readSkillBody(path)

	require.NoError(t, err)
	assert.Contains(t, body, "--- not a closing marker")
}

func assertContainsDiagnostic(t *testing.T, diagnostics []Diagnostic, substr string) {
	t.Helper()
	for _, diag := range diagnostics {
		if strings.Contains(diag.Message, substr) {
			return
		}
	}
	t.Fatalf("expected diagnostic containing %q, got %#v", substr, diagnostics)
}
