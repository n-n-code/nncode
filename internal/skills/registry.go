package skills

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	maxFrontmatterBytes   = 64 * 1024
	defaultResourceCap    = 20
	maxCatalogSkills      = 64
	maxCatalogBytes       = 12 * 1024
	activationMarkerOpen  = "<activated_skill>"
	activationMarkerClose = "</activated_skill>"
	frontmatterMarker     = "---"
)

var (
	errMissingOpeningFrontmatter = errors.New("missing opening frontmatter marker")
	errMissingClosingFrontmatter = errors.New("missing closing frontmatter marker")
	errFrontmatterTooLarge       = errors.New("frontmatter exceeds maximum size")
	errInvalidFrontmatterLine    = errors.New("invalid frontmatter line")
	errEmptyFrontmatterKey       = errors.New("empty frontmatter key")
	errSkillsNotConfigured       = errors.New("skills are not configured")
	errSkillNameRequired         = errors.New("skill name is required")
	errUnknownSkill              = errors.New("unknown skill")
	errSkillManualOnly           = errors.New("skill is manual-only and is not available for model activation")
)

// DiagnosticLevel classifies a skill discovery diagnostic.
type DiagnosticLevel string

const (
	DiagnosticInfo  DiagnosticLevel = "info"
	DiagnosticWarn  DiagnosticLevel = "warn"
	DiagnosticError DiagnosticLevel = "error"
)

// Diagnostic is a non-fatal discovery note shown by /skills.
type Diagnostic struct {
	Level   DiagnosticLevel
	Path    string
	Message string
}

// Skill is the frontmatter-only metadata discovered at startup.
type Skill struct {
	Name                   string
	Description            string
	Dir                    string
	Path                   string
	Source                 string
	DisableModelInvocation bool
}

// Catalog is the bounded model-visible subset advertised in the prompt and
// activate_skill tool schema.
type Catalog struct {
	Skills  []Skill
	Lines   []string
	Omitted int
}

// Names returns the skill names included in the catalog.
func (c Catalog) Names() []string {
	out := make([]string, len(c.Skills))
	for i, skill := range c.Skills {
		out[i] = skill.Name
	}

	return out
}

// Contains reports whether name is included in the catalog.
func (c Catalog) Contains(name string) bool {
	for _, skill := range c.Skills {
		if skill.Name == name {
			return true
		}
	}

	return false
}

// Registry contains discovered skills and diagnostics.
type Registry struct {
	skills                 map[string]Skill
	diagnostics            []Diagnostic
	catalogDiagnosticAdded bool
}

// DiscoverOptions controls where discovery starts. Empty values use the
// process working directory and current user home.
type DiscoverOptions struct {
	CWD     string
	HomeDir string
}

// Discover scans project .agents/skills directories from cwd to the git root,
// then ~/.agents/skills at lower precedence.
func Discover(opts DiscoverOptions) *Registry {
	reg := &Registry{skills: make(map[string]Skill)}

	cwd := opts.CWD
	if cwd == "" {
		var err error

		cwd, err = os.Getwd()
		if err != nil {
			reg.add(DiagnosticError, "", fmt.Sprintf("resolve working directory: %v", err))

			return reg
		}
	}

	cwd, err := filepath.Abs(cwd)
	if err != nil {
		reg.add(DiagnosticError, cwd, fmt.Sprintf("resolve working directory: %v", err))

		return reg
	}

	for _, dir := range projectSkillDirs(cwd) {
		reg.scanDir(dir, "project")
	}

	home := opts.HomeDir
	if home == "" {
		var err error

		home, err = os.UserHomeDir()
		if err != nil {
			reg.add(DiagnosticWarn, "", fmt.Sprintf("resolve home directory: %v", err))

			return reg
		}
	}

	if home != "" {
		reg.scanDir(filepath.Join(home, ".agents", "skills"), "global")
	}

	return reg
}

// Skills returns all discovered skills sorted by name.
func (r *Registry) Skills() []Skill {
	if r == nil {
		return nil
	}

	out := make([]Skill, 0, len(r.skills))
	for _, skill := range r.skills {
		out = append(out, skill)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// ModelVisibleSkills returns skills available for automatic model activation.
func (r *Registry) ModelVisibleSkills() []Skill {
	all := r.Skills()

	out := all[:0]

	for _, skill := range all {
		if !skill.DisableModelInvocation {
			out = append(out, skill)
		}
	}

	return out
}

// ModelVisibleNames returns visible skill names sorted by name.
func (r *Registry) ModelVisibleNames() []string {
	skills := r.ModelVisibleSkills()

	out := make([]string, len(skills))
	for i, skill := range skills {
		out[i] = skill.Name
	}

	return out
}

// Lookup finds a skill by frontmatter name.
func (r *Registry) Lookup(name string) (Skill, bool) {
	if r == nil {
		return Skill{}, false
	}

	skill, ok := r.skills[name]

	return skill, ok
}

// Diagnostics returns discovery diagnostics in the order they were produced.
func (r *Registry) Diagnostics() []Diagnostic {
	if r == nil {
		return nil
	}

	return append([]Diagnostic(nil), r.diagnostics...)
}

// ModelCatalog returns the bounded model-visible skills advertised for
// automatic activation.
func (r *Registry) ModelCatalog() Catalog {
	if r == nil {
		return Catalog{}
	}

	catalog := buildCatalog(r.ModelVisibleSkills())
	r.addCatalogDiagnostic(catalog.Omitted)

	return catalog
}

func (r *Registry) scanDir(skillsDir string, source string) {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			r.add(DiagnosticWarn, skillsDir, fmt.Sprintf("read skills directory: %v", err))
		}

		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dir := filepath.Join(skillsDir, entry.Name())
		path := filepath.Join(dir, "SKILL.md")

		skill, diagnostics, ok := readSkillMetadata(path, dir, source)
		for _, diag := range diagnostics {
			r.add(diag.Level, diag.Path, diag.Message)
		}

		if !ok {
			continue
		}

		if existing, exists := r.skills[skill.Name]; exists {
			msg := fmt.Sprintf(
				"skill %q ignored because %s already defines it",
				skill.Name, existing.Path,
			)
			r.add(DiagnosticWarn, path, msg)

			continue
		}

		r.skills[skill.Name] = skill
	}
}

func (r *Registry) add(level DiagnosticLevel, path string, message string) {
	r.diagnostics = append(r.diagnostics, Diagnostic{Level: level, Path: path, Message: message})
}

func (r *Registry) addCatalogDiagnostic(omitted int) {
	if r == nil || r.catalogDiagnosticAdded || omitted <= 0 {
		return
	}

	r.catalogDiagnosticAdded = true
	msg := fmt.Sprintf(
		"model activation skill catalog truncated; %d skills omitted from the prompt and activate_skill enum",
		omitted,
	)
	r.add(DiagnosticWarn, "", msg)
}

func projectSkillDirs(cwd string) []string {
	root := findGitRoot(cwd)
	if root == "" {
		return []string{filepath.Join(cwd, ".agents", "skills")}
	}

	var dirs []string

	for dir := cwd; ; dir = filepath.Dir(dir) {
		dirs = append(dirs, filepath.Join(dir, ".agents", "skills"))

		if samePath(dir, root) {
			break
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}

	return dirs
}

func findGitRoot(start string) string {
	for dir := start; ; dir = filepath.Dir(dir) {
		_, err := os.Stat(filepath.Join(dir, ".git"))
		if err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
	}
}

func samePath(a string, b string) bool {
	rel, err := filepath.Rel(a, b)

	return err == nil && rel == "."
}

type frontmatter struct {
	Name                   string
	Description            string
	DisableModelInvocation bool
	Diagnostics            []Diagnostic
}

func readSkillMetadata(path string, dir string, source string) (Skill, []Diagnostic, bool) {
	raw, err := readFrontmatter(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Skill{}, nil, false
		}

		msg := fmt.Sprintf("read frontmatter: %v", err)

		return Skill{}, []Diagnostic{{Level: DiagnosticError, Path: path, Message: msg}}, false
	}

	parsedFrontmatter, err := parseFrontmatter(raw)
	if err != nil {
		msg := fmt.Sprintf("parse frontmatter: %v", err)

		return Skill{}, []Diagnostic{{Level: DiagnosticError, Path: path, Message: msg}}, false
	}

	var diagnostics []Diagnostic

	diagnostics = append(diagnostics, parsedFrontmatter.Diagnostics...)
	for i := range diagnostics {
		diagnostics[i].Path = path
	}

	name := strings.TrimSpace(parsedFrontmatter.Name)
	description := normalizeWhitespace(parsedFrontmatter.Description)

	if name == "" {
		diagnostics = append(diagnostics, Diagnostic{
			Level: DiagnosticError, Path: path,
			Message: "missing required frontmatter field: name",
		})

		return Skill{}, diagnostics, false
	}

	if description == "" {
		diagnostics = append(diagnostics, Diagnostic{
			Level: DiagnosticError, Path: path,
			Message: "missing required frontmatter field: description",
		})

		return Skill{}, diagnostics, false
	}

	if filepath.Base(dir) != name {
		msg := fmt.Sprintf("directory name %q differs from skill name %q", filepath.Base(dir), name)
		diagnostics = append(diagnostics, Diagnostic{Level: DiagnosticWarn, Path: path, Message: msg})
	}

	return Skill{
		Name:                   name,
		Description:            description,
		Dir:                    dir,
		Path:                   path,
		Source:                 source,
		DisableModelInvocation: parsedFrontmatter.DisableModelInvocation,
	}, diagnostics, true
}
func readFrontmatter(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open skill file: %w", err)
	}
	defer func() { _ = file.Close() }()

	reader := bufio.NewReader(file)

	first, err := reader.ReadString('\n')
	if err != nil {
		return "", errMissingOpeningFrontmatter
	}

	if strings.TrimSpace(first) != frontmatterMarker {
		return "", errMissingOpeningFrontmatter
	}

	var builder strings.Builder

	bytesRead := len(first)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", errMissingClosingFrontmatter
		}

		bytesRead += len(line)
		if bytesRead > maxFrontmatterBytes {
			return "", fmt.Errorf("%w: %d bytes", errFrontmatterTooLarge, maxFrontmatterBytes)
		}

		if strings.TrimSpace(line) == frontmatterMarker {
			return builder.String(), nil
		}

		builder.WriteString(line)
	}
}

func parseFrontmatter(raw string) (frontmatter, error) {
	lines := strings.Split(raw, "\n")
	values := make(map[string]string)

	var diagnostics []Diagnostic

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") ||
			strings.HasPrefix(trimmed, "- ") {
			msg := fmt.Sprintf("unsupported nested frontmatter line ignored: %q", trimmed)
			diagnostics = append(diagnostics, Diagnostic{Level: DiagnosticWarn, Message: msg})

			continue
		}

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return frontmatter{}, fmt.Errorf("%w %q", errInvalidFrontmatterLine, trimmed)
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		if key == "" {
			return frontmatter{}, errEmptyFrontmatterKey
		}

		switch {
		case isBlockScalar(value):
			block, next := collectBlock(lines, i+1, strings.HasPrefix(value, ">"))
			value = block
			i = next - 1
		case value == "":
			next := skipIndented(lines, i+1)
			if next > i+1 {
				msg := fmt.Sprintf("unsupported nested value for frontmatter field %q ignored", key)
				diagnostics = append(diagnostics, Diagnostic{Level: DiagnosticWarn, Message: msg})
				i = next - 1
			}
		default:
			value = unquoteYAMLString(value)
		}

		if _, exists := values[key]; exists {
			msg := fmt.Sprintf("duplicate frontmatter field %q; using last value", key)
			diagnostics = append(diagnostics, Diagnostic{Level: DiagnosticWarn, Message: msg})
		}

		if !isSupportedFrontmatterField(key) {
			msg := fmt.Sprintf("unsupported frontmatter field %q ignored", key)
			diagnostics = append(diagnostics, Diagnostic{Level: DiagnosticWarn, Message: msg})

			continue
		}

		values[key] = value
	}

	parsed := frontmatter{
		Name:        values["name"],
		Description: values["description"],
		Diagnostics: diagnostics,
	}

	if rawBool, ok := values["disable-model-invocation"]; ok && strings.TrimSpace(rawBool) != "" {
		value, err := strconv.ParseBool(strings.TrimSpace(rawBool))
		if err != nil {
			parsed.Diagnostics = append(parsed.Diagnostics, Diagnostic{
				Level:   DiagnosticWarn,
				Message: "disable-model-invocation should be true or false; treating as false",
			})
		} else {
			parsed.DisableModelInvocation = value
		}
	}

	return parsed, nil
}

func isSupportedFrontmatterField(key string) bool {
	switch key {
	case "name", "description", "disable-model-invocation":
		return true
	default:
		return false
	}
}

func isBlockScalar(value string) bool {
	switch value {
	case "|", "|-", "|+", ">", ">-", ">+":
		return true
	default:
		return false
	}
}

func collectBlock(lines []string, start int, folded bool) (string, int) {
	var block []string

	for i := start; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			block = append(block, "")

			continue
		}

		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			return formatBlock(block, folded), i
		}

		block = append(block, strings.TrimSpace(line))
	}

	return formatBlock(block, folded), len(lines)
}

func skipIndented(lines []string, start int) int {
	for i := start; i < len(lines); i++ {
		line := lines[i]

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") &&
			!strings.HasPrefix(trimmed, "- ") {
			return i
		}
	}

	return len(lines)
}

func formatBlock(lines []string, folded bool) string {
	if folded {
		return strings.Join(lines, " ")
	}

	return strings.Join(lines, "\n")
}

func unquoteYAMLString(value string) string {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		unquoted, err := strconv.Unquote(value)
		if err == nil {
			return unquoted
		}
	}

	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'")
	}

	return value
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// Activation is the full skill payload loaded on demand.
type Activation struct {
	Skill              Skill
	Content            string
	Resources          []string
	ResourcesTruncated int
	Duplicate          bool
}

// Activator tracks skills already loaded into the current session.
type Activator struct {
	registry  *Registry
	activated map[string]struct{}
	mu        sync.Mutex
}

// NewActivator creates a session-scoped skill activator.
func NewActivator(registry *Registry) *Activator {
	return &Activator{registry: registry, activated: make(map[string]struct{})}
}

// Registry returns the underlying registry.
func (a *Activator) Registry() *Registry {
	if a == nil {
		return nil
	}

	return a.registry
}

// Reset forgets session activation state.
func (a *Activator) Reset() {
	if a == nil {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.activated = make(map[string]struct{})
}

// Activate loads a skill. Hidden skills require allowHidden=true.
func (a *Activator) Activate(name string, allowHidden bool) (Activation, error) {
	if a == nil || a.registry == nil {
		return Activation{}, errSkillsNotConfigured
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return Activation{}, errSkillNameRequired
	}

	skill, ok := a.registry.Lookup(name)
	if !ok {
		return Activation{}, fmt.Errorf("%w: %q", errUnknownSkill, name)
	}

	if skill.DisableModelInvocation && !allowHidden {
		return Activation{}, fmt.Errorf("%w: %q", errSkillManualOnly, name)
	}

	a.mu.Lock()
	if _, ok := a.activated[name]; ok {
		a.mu.Unlock()

		return Activation{Skill: skill, Duplicate: true}, nil
	}

	a.activated[name] = struct{}{}
	a.mu.Unlock()

	body, err := readSkillBody(skill.Path)
	if err != nil {
		a.mu.Lock()
		delete(a.activated, name)
		a.mu.Unlock()

		return Activation{}, err
	}

	resources, truncated, err := listResources(skill.Dir, defaultResourceCap)
	if err != nil {
		a.mu.Lock()
		delete(a.activated, name)
		a.mu.Unlock()

		return Activation{}, err
	}

	return Activation{Skill: skill, Content: body, Resources: resources, ResourcesTruncated: truncated}, nil
}

// MarkActivated records a skill as already loaded, if it exists.
func (a *Activator) MarkActivated(name string) {
	if a == nil || a.registry == nil {
		return
	}

	name = strings.TrimSpace(name)
	if _, ok := a.registry.Lookup(name); !ok {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.activated[name] = struct{}{}
}

// MarkActivatedFromText records any formatted activation found in text.
func (a *Activator) MarkActivatedFromText(text string) {
	if a == nil || a.registry == nil || text == "" {
		return
	}

	for _, name := range activationMarkerNames(text) {
		a.MarkActivated(name)
	}

	for _, skill := range a.registry.Skills() {
		if strings.Contains(text, fmt.Sprintf("Skill %q activated.", skill.Name)) {
			a.MarkActivated(skill.Name)
		}
	}
}

func readSkillBody(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read skill: %w", err)
	}

	_, body, err := splitSkillDocument(string(data))
	if err != nil {
		return "", fmt.Errorf("parse skill document: %w", err)
	}

	return strings.TrimSpace(body), nil
}

func splitSkillDocument(text string) (string, string, error) {
	text = strings.TrimPrefix(text, "\ufeff")

	firstEnd := strings.IndexByte(text, '\n')
	if firstEnd < 0 {
		return "", "", errMissingOpeningFrontmatter
	}

	if strings.TrimSpace(strings.TrimRight(text[:firstEnd], "\r")) != frontmatterMarker {
		return "", "", errMissingOpeningFrontmatter
	}

	offset := firstEnd + 1
	for offset <= len(text) {
		next := strings.IndexByte(text[offset:], '\n')
		lineStart := offset
		lineEnd := len(text)
		afterLine := len(text)

		if next >= 0 {
			lineEnd = offset + next
			afterLine = lineEnd + 1
		}

		line := strings.TrimRight(text[lineStart:lineEnd], "\r")
		if strings.TrimSpace(line) == frontmatterMarker {
			frontmatter := text[firstEnd+1 : lineStart]
			body := strings.TrimLeft(text[afterLine:], "\r\n")

			return frontmatter, body, nil
		}

		if next < 0 {
			break
		}

		offset = afterLine
	}

	return "", "", errMissingClosingFrontmatter
}

func listResources(dir string, limit int) ([]string, int, error) {
	if limit <= 0 {
		limit = defaultResourceCap
	}

	var resources []string

	total := 0

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("resolve relative path: %w", err)
		}

		if rel == "SKILL.md" {
			return nil
		}

		total++

		if len(resources) < limit {
			resources = append(resources, filepath.ToSlash(rel))
		}

		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list skill resources: %w", err)
	}

	return resources, max(total-len(resources), 0), nil
}

// FormatActivation renders a skill payload for the model.
func FormatActivation(act Activation) string {
	if act.Duplicate {
		return fmt.Sprintf(
			"Skill %q is already active in this session. "+
				"Reuse the previously loaded instructions.",
			act.Skill.Name,
		)
	}

	var builder strings.Builder

	builder.WriteString(formatActivationMarker(act.Skill.Name))
	builder.WriteByte('\n')
	fmt.Fprintf(&builder, "Skill %q activated.\n", act.Skill.Name)
	fmt.Fprintf(&builder, "Skill directory: %s\n", act.Skill.Dir)

	if len(act.Resources) == 0 && act.ResourcesTruncated == 0 {
		builder.WriteString("Resources: none\n")
	} else {
		builder.WriteString("Resources:\n")

		for _, resource := range act.Resources {
			fmt.Fprintf(&builder, "- %s\n", resource)
		}

		if act.ResourcesTruncated > 0 {
			fmt.Fprintf(&builder, "- ... (%d more)\n", act.ResourcesTruncated)
		}
	}

	builder.WriteString("<skill_content>\n")
	builder.WriteString(act.Content)
	builder.WriteString("\n</skill_content>")

	return builder.String()
}

func formatActivationMarker(name string) string {
	data, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return activationMarkerOpen + activationMarkerClose
	}

	return activationMarkerOpen + string(data) + activationMarkerClose
}

func activationMarkerNames(text string) []string {
	var names []string

	remaining := text

	for {
		start := strings.Index(remaining, activationMarkerOpen)
		if start < 0 {
			return names
		}

		remaining = remaining[start+len(activationMarkerOpen):]

		end := strings.Index(remaining, activationMarkerClose)
		if end < 0 {
			return names
		}

		var payload struct {
			Name string `json:"name"`
		}

		err := json.Unmarshal([]byte(strings.TrimSpace(remaining[:end])), &payload)
		if err == nil && strings.TrimSpace(payload.Name) != "" {
			names = append(names, payload.Name)
		}

		remaining = remaining[end+len(activationMarkerClose):]
	}
}

// StripActivationMarkers removes structured activation markers from display
// text while preserving the persisted message content elsewhere.
func StripActivationMarkers(text string) string {
	var builder strings.Builder

	remaining := text

	for {
		start := strings.Index(remaining, activationMarkerOpen)
		if start < 0 {
			builder.WriteString(remaining)

			return strings.TrimSpace(builder.String())
		}

		builder.WriteString(remaining[:start])
		afterOpen := remaining[start+len(activationMarkerOpen):]

		_, after, ok := strings.Cut(afterOpen, activationMarkerClose)
		if !ok {
			builder.WriteString(remaining[start:])

			return strings.TrimSpace(builder.String())
		}

		remaining = after
	}
}

// ComposeSystemPrompt appends the visible skill catalog to base.
func ComposeSystemPrompt(base string, registry *Registry) string {
	if registry == nil || len(registry.ModelVisibleSkills()) == 0 {
		return base
	}

	catalog := registry.ModelCatalog()

	var builder strings.Builder

	builder.WriteString(strings.TrimRight(base, "\n"))

	if builder.Len() > 0 {
		builder.WriteString("\n\n")
	}

	builder.WriteString("<available_skills>\n")
	builder.WriteString(
		"Agent Skills are available. The catalog contains names and descriptions only; " +
			"call activate_skill with the exact name before applying a relevant skill.\n",
	)

	for _, line := range catalog.Lines {
		builder.WriteString(line)
		builder.WriteByte('\n')
	}

	if catalog.Omitted > 0 {
		fmt.Fprintf(
			&builder,
			"- ... (%d more skills omitted; use /skills to inspect the full local catalog)\n",
			catalog.Omitted,
		)
	}

	builder.WriteString("</available_skills>")

	return builder.String()
}

func buildCatalog(skills []Skill) Catalog {
	catalog := Catalog{}
	used := 0

	for i, skill := range skills {
		if len(catalog.Skills) >= maxCatalogSkills {
			catalog.Omitted = len(skills) - i

			return catalog
		}

		line := fmt.Sprintf("- %s: %s", skill.Name, skill.Description)

		lineBytes := len(line) + 1
		if used+lineBytes > maxCatalogBytes {
			catalog.Omitted = len(skills) - i

			return catalog
		}

		catalog.Skills = append(catalog.Skills, skill)
		catalog.Lines = append(catalog.Lines, line)
		used += lineBytes
	}

	return catalog
}
