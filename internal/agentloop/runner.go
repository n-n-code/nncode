package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"nncode/internal/agent"
	"nncode/internal/config"
	"nncode/internal/llm"
	builtintools "nncode/internal/tools"
)

const (
	eventBufferSize       = 32
	entryInputPlaceholder = "{{input}}"
	exitCriteriaWarning   = "\n[loop warning] exit criteria did not include LOOP_EXIT: yes or LOOP_EXIT: no; continuing.\n"
)

var (
	errRunnerAgentRequired  = errors.New("agent loop runner requires an agent")
	errRunnerConfigRequired = errors.New("agent loop runner requires config for model overrides")
	errLoopModelUnknown     = errors.New("loop model is not configured")
	errLoopMaxIterations    = errors.New("loop max_iterations exceeded")
	errLoopCmdFailed        = errors.New("cmd node failed")
	errLoopCmdDisabled      = errors.New("cmd node requires bash, but bash is disabled")
)

type Runner struct {
	Agent        *agent.Agent
	Config       *config.Config
	StoreOptions StoreOptions
}

func (r Runner) Run(ctx context.Context, ref string, input string) (<-chan agent.Event, error) {
	if r.Agent == nil {
		return nil, errRunnerAgentRequired
	}

	def, path, err := Load(ref, r.StoreOptions)
	if err != nil {
		return nil, err
	}

	locked := snapshotLockedNodes(def)
	loopContext := formatLoopSystemMessage(def, path, locked)

	out := make(chan agent.Event, eventBufferSize)
	go func() {
		defer close(out)
		if !emit(ctx, out, agent.Event{
			Type:     agent.EventLoopStart,
			LoopName: def.Name,
			LoopPath: path,
		}) {
			return
		}

		r.execute(ctx, out, path, def, locked, input, loopContext)
	}()

	return out, nil
}

func (r Runner) execute(
	ctx context.Context,
	out chan<- agent.Event,
	path string,
	def Definition,
	locked map[string]Node,
	input string,
	loopContext string,
) {
	current := def
	current, err := r.runEntry(ctx, out, path, current, locked, input, loopContext)
	if err != nil {
		emitError(ctx, out, err)

		return
	}

	for iteration := 1; ; iteration++ {
		maxIter := current.maxIterations()
		if iteration > maxIter {
			emitError(ctx, out, fmt.Errorf(
				"%w: loop %q exceeded max_iterations (%d)",
				errLoopMaxIterations,
				current.Name,
				maxIter,
			))

			return
		}

		if !emit(ctx, out, agent.Event{
			Type:          agent.EventLoopIterationStart,
			LoopName:      current.Name,
			LoopIteration: iteration,
		}) {
			return
		}

		var shouldExit bool
		current, shouldExit, err = r.runIteration(ctx, out, path, current, locked, loopContext, iteration)
		if err != nil {
			emitError(ctx, out, err)

			return
		}

		if shouldExit {
			if err := r.runExitPrompt(ctx, out, path, current, locked, loopContext); err != nil {
				emitError(ctx, out, err)

				return
			}

			return
		}
	}
}

func (r Runner) runEntry(
	ctx context.Context,
	out chan<- agent.Event,
	path string,
	def Definition,
	locked map[string]Node,
	input string,
	loopContext string,
) (Definition, error) {
	entry := def.entryNode()
	if _, err := r.runNode(ctx, out, def, entry, formatEntryPrompt(entry, input), false, loopContext); err != nil {
		return Definition{}, err
	}

	return reloadAndProtect(path, locked)
}

func (r Runner) runIteration(
	ctx context.Context,
	out chan<- agent.Event,
	path string,
	def Definition,
	locked map[string]Node,
	loopContext string,
	iteration int,
) (Definition, bool, error) {
	current, err := r.runBodyNodes(ctx, out, path, def, locked, loopContext)
	if err != nil {
		return Definition{}, false, err
	}

	return r.runExitCheck(ctx, out, path, current, locked, loopContext, iteration)
}

func (r Runner) runBodyNodes(
	ctx context.Context,
	out chan<- agent.Event,
	path string,
	def Definition,
	locked map[string]Node,
	loopContext string,
) (Definition, error) {
	current := def
	for _, node := range current.bodyNodes() {
		//nolint:exhaustive // bodyNodes returns only prompt and cmd nodes.
		switch node.Type {
		case NodePrompt:
			if _, err := r.runNode(ctx, out, current, node, formatPromptNode(node), false, loopContext); err != nil {
				return Definition{}, err
			}
		case NodeCmd:
			if err := r.runCommandNode(ctx, out, current, node); err != nil {
				return Definition{}, err
			}
		default:
			return Definition{}, fmt.Errorf("%w in loop body: %q", errLoopNodeTypeUnknown, node.Type)
		}

		var err error
		current, err = reloadAndProtect(path, locked)
		if err != nil {
			return Definition{}, err
		}
	}

	return current, nil
}

func (r Runner) runExitCheck(
	ctx context.Context,
	out chan<- agent.Event,
	path string,
	def Definition,
	locked map[string]Node,
	loopContext string,
	iteration int,
) (Definition, bool, error) {
	exitNode := def.exitCriteriaNode()
	rawExitText, err := r.runNode(ctx, out, def, exitNode, formatExitCriteriaNode(exitNode), true, loopContext)
	if err != nil {
		return Definition{}, false, err
	}

	shouldExit, markerOK, cleanedText := parseExitDecision(rawExitText)
	if !emit(ctx, out, agent.Event{
		Type:                agent.EventLoopExitDecision,
		LoopName:            def.Name,
		LoopIteration:       iteration,
		LoopNodeID:          exitNode.ID,
		LoopNodeType:        string(exitNode.Type),
		LoopExit:            shouldExit,
		LoopExitMarkerFound: markerOK,
	}) {
		return Definition{}, false, fmt.Errorf("emit exit decision: %w", ctx.Err())
	}

	if !emitExitText(ctx, out, cleanedText) {
		return Definition{}, false, fmt.Errorf("emit exit text: %w", ctx.Err())
	}

	if !markerOK && !emit(ctx, out, agent.Event{
		Type: agent.EventText,
		Text: exitCriteriaWarning,
	}) {
		return Definition{}, false, fmt.Errorf("emit exit warning: %w", ctx.Err())
	}

	current, err := reloadAndProtect(path, locked)
	if err != nil {
		return Definition{}, false, err
	}

	return current, shouldExit, nil
}

func (r Runner) runExitPrompt(
	ctx context.Context,
	out chan<- agent.Event,
	path string,
	def Definition,
	locked map[string]Node,
	loopContext string,
) error {
	exitPrompt, ok := def.exitPromptNode()
	if !ok {
		return nil
	}

	if _, err := r.runNode(ctx, out, def, exitPrompt, formatPromptNode(exitPrompt), false, loopContext); err != nil {
		return err
	}

	_, err := reloadAndProtect(path, locked)

	return err
}

func emitExitText(ctx context.Context, out chan<- agent.Event, cleanedText string) bool {
	if strings.TrimSpace(cleanedText) == "" {
		return true
	}

	text := cleanedText
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}

	return emit(ctx, out, agent.Event{Type: agent.EventText, Text: text})
}

func (r Runner) runNode(
	ctx context.Context,
	out chan<- agent.Event,
	def Definition,
	node Node,
	prompt string,
	bufferText bool,
	loopContext string,
) (string, error) {
	runOpts, err := r.runOptions(def, node, loopContext)
	if err != nil {
		return "", err
	}

	if !emit(ctx, out, agent.Event{
		Type:         agent.EventLoopNodeStart,
		LoopName:     def.Name,
		LoopNodeID:   node.ID,
		LoopNodeType: string(node.Type),
	}) {
		return "", fmt.Errorf("emit loop node start: %w", ctx.Err())
	}

	events := r.Agent.RunWithOptions(ctx, prompt, runOpts)
	var text strings.Builder
	var eventErr error

	for ev := range events {
		if ev.Type == agent.EventText {
			text.WriteString(ev.Text)
			if bufferText {
				continue
			}
		}

		if bufferText && ev.Type == agent.EventDone {
			continue
		}

		if ev.Type == agent.EventError {
			eventErr = ev.Err

			continue
		}

		if !emit(ctx, out, ev) {
			return text.String(), ctx.Err()
		}
	}

	if !emit(ctx, out, agent.Event{
		Type:         agent.EventLoopNodeEnd,
		LoopName:     def.Name,
		LoopNodeID:   node.ID,
		LoopNodeType: string(node.Type),
	}) {
		return text.String(), fmt.Errorf("emit loop node end: %w", ctx.Err())
	}

	return text.String(), eventErr
}

func (r Runner) runCommandNode(ctx context.Context, out chan<- agent.Event, def Definition, node Node) error {
	command := strings.TrimSpace(node.Content)

	if !emit(ctx, out, agent.Event{
		Type:         agent.EventLoopNodeStart,
		LoopName:     def.Name,
		LoopNodeID:   node.ID,
		LoopNodeType: string(node.Type),
	}) {
		return fmt.Errorf("emit loop node start: %w", ctx.Err())
	}

	if !emit(ctx, out, agent.Event{
		Type:     agent.EventToolCallStart,
		ToolID:   node.ID,
		ToolName: "cmd",
	}) {
		return fmt.Errorf("emit cmd start: %w", ctx.Err())
	}

	if !emit(ctx, out, agent.Event{
		Type:     agent.EventToolCallEnd,
		ToolID:   node.ID,
		ToolName: "cmd",
		ToolArgs: formatCommandArgs(node.ID, command),
	}) {
		return fmt.Errorf("emit cmd command: %w", ctx.Err())
	}

	result := r.executeCommandNode(ctx, node, command)

	if !emit(ctx, out, agent.Event{
		Type:     agent.EventToolResult,
		ToolID:   node.ID,
		ToolName: "cmd",
		Result:   result.Content,
		IsError:  result.IsError,
		Metadata: result.Metadata,
	}) {
		return fmt.Errorf("emit cmd result: %w", ctx.Err())
	}

	r.Agent.AddObservationMessage(formatCommandObservation(node, command, result))

	if !emit(ctx, out, agent.Event{
		Type:         agent.EventLoopNodeEnd,
		LoopName:     def.Name,
		LoopNodeID:   node.ID,
		LoopNodeType: string(node.Type),
	}) {
		return fmt.Errorf("emit loop node end: %w", ctx.Err())
	}

	if result.IsError && node.onError() == OnErrorAbort {
		return fmt.Errorf("%w: %q", errLoopCmdFailed, node.ID)
	}

	return nil
}

func (r Runner) executeCommandNode(ctx context.Context, node Node, command string) agent.ToolResult {
	if r.Config != nil && r.Config.Tools.IsDisabled("bash") {
		return commandErrorResult(node, fmt.Errorf("%w: %q", errLoopCmdDisabled, node.ID))
	}

	if r.Agent.DryRun() {
		return agent.ToolResult{
			Content: fmt.Sprintf("[dry-run] Would execute cmd node %q with command: %s", node.ID, command),
			Metadata: map[string]any{
				"dry_run": true,
				"node_id": node.ID,
				"tool":    "cmd",
			},
		}
	}

	result, err := builtintools.RunBashCommand(ctx, command, r.toolOptions())
	if err != nil {
		return commandErrorResult(node, fmt.Errorf("run cmd node %q: %w", node.ID, err))
	}

	if result.Metadata == nil {
		result.Metadata = make(map[string]any)
	}

	result.Metadata["node_id"] = node.ID
	result.Metadata["tool"] = "cmd"

	return result
}

func commandErrorResult(node Node, err error) agent.ToolResult {
	return agent.ToolResult{
		Content: err.Error(),
		IsError: true,
		Metadata: map[string]any{
			"node_id": node.ID,
			"tool":    "cmd",
		},
	}
}

func (r Runner) toolOptions() builtintools.Options {
	if r.Config == nil {
		return builtintools.Options{}
	}

	return builtintools.Options{
		RootDir:            r.Config.Tools.WorkspaceRoot,
		MaxReadBytes:       r.Config.Tools.MaxReadBytes,
		MaxWriteBytes:      r.Config.Tools.MaxWriteBytes,
		MaxBashOutputBytes: r.Config.Tools.MaxBashOutputBytes,
		BashTimeout:        time.Duration(r.Config.Tools.BashTimeoutSeconds) * time.Second,
	}
}

func formatCommandArgs(nodeID string, command string) string {
	return `{"node_id":` + strconv.Quote(nodeID) + `,"command":` + strconv.Quote(command) + `}`
}

func formatCommandObservation(node Node, command string, result agent.ToolResult) string {
	var builder strings.Builder
	builder.WriteString("Agent loop cmd node ")
	builder.WriteString(strconv.Quote(node.ID))
	builder.WriteString(" result:\n")
	builder.WriteString("command: ")
	builder.WriteString(command)
	builder.WriteString("\n")
	writeCommandMetadata(&builder, result.Metadata)
	builder.WriteString("\nOutput:\n")
	if strings.TrimSpace(result.Content) == "" {
		builder.WriteString("(no output)")
	} else {
		builder.WriteString(result.Content)
	}

	return builder.String()
}

func writeCommandMetadata(builder *strings.Builder, metadata map[string]any) {
	if metadata == nil {
		return
	}

	if value, ok := metadata["exit_code"]; ok {
		builder.WriteString("exit_code: ")
		fmt.Fprint(builder, value)
		builder.WriteString("\n")
	}

	if value, ok := metadata["duration_ms"]; ok {
		builder.WriteString("duration_ms: ")
		fmt.Fprint(builder, value)
		builder.WriteString("\n")
	}

	if value, ok := metadata["truncated"]; ok {
		builder.WriteString("truncated: ")
		fmt.Fprint(builder, value)
		builder.WriteString("\n")
	}

	if value, ok := metadata["dry_run"]; ok {
		builder.WriteString("dry_run: ")
		fmt.Fprint(builder, value)
		builder.WriteString("\n")
	}
}

func (r Runner) runOptions(def Definition, node Node, loopContext string) (agent.RunOptions, error) {
	modelName := strings.TrimSpace(def.Settings.Model)
	if nodeModel := strings.TrimSpace(node.Settings.Model); nodeModel != "" {
		modelName = nodeModel
	}

	scopedMessages := []string{loopContext}

	if modelName == "" {
		return agent.RunOptions{ScopedSystemMessages: scopedMessages}, nil
	}

	if r.Config == nil {
		return agent.RunOptions{}, errRunnerConfigRequired
	}

	r.Config.AutoVendModel(modelName)
	modelCfg, ok := r.Config.ResolveModel(modelName)
	if !ok {
		return agent.RunOptions{}, fmt.Errorf("%w: %q", errLoopModelUnknown, modelName)
	}

	if err := modelCfg.Validate(modelName); err != nil {
		return agent.RunOptions{}, fmt.Errorf("validate loop model %q: %w", modelName, err)
	}

	return agent.RunOptions{
		Model:                llm.Model{ID: modelCfg.RequestID(modelName), BaseURL: modelCfg.BaseURL},
		MaxTokens:            modelCfg.MaxTokens,
		ScopedSystemMessages: scopedMessages,
	}, nil
}

func formatEntryPrompt(node Node, input string) string {
	content := node.Content
	if strings.Contains(content, entryInputPlaceholder) {
		content = strings.ReplaceAll(content, entryInputPlaceholder, input)
	} else if strings.TrimSpace(input) != "" {
		if strings.TrimSpace(content) == "" {
			content = "User input:\n" + input
		} else {
			content = strings.TrimRight(content, "\n") + "\n\nUser input:\n" + input
		}
	}

	return formatNodeEnvelope(node, content)
}

func formatPromptNode(node Node) string {
	return formatNodeEnvelope(node, node.Content)
}

func formatExitCriteriaNode(node Node) string {
	var builder strings.Builder
	builder.WriteString("Exit criteria:\n")
	builder.WriteString(node.Content)
	builder.WriteString("\n\nCheck whether the current work in this session satisfies the exit criteria. ")
	builder.WriteString("Include one line exactly `LOOP_EXIT: yes` if the loop should exit, ")
	builder.WriteString("or `LOOP_EXIT: no` if it should continue.")

	return formatNodeEnvelope(node, builder.String())
}

func formatNodeEnvelope(node Node, content string) string {
	return fmt.Sprintf("Agent loop node %q (%s):\n\n%s", node.ID, node.Type, content)
}

func parseExitDecision(text string) (bool, bool, string) {
	lines := strings.Split(text, "\n")
	cleaned := make([]string, 0, len(lines))
	var decision bool
	var found bool

	for _, line := range lines {
		normalized := strings.ToLower(strings.Trim(strings.TrimSpace(line), "`*_ "))
		switch normalized {
		case "loop_exit: yes":
			decision = true
			found = true

			continue
		case "loop_exit: no":
			decision = false
			found = true

			continue
		}

		cleaned = append(cleaned, line)
	}

	return decision, found, strings.TrimSpace(strings.Join(cleaned, "\n"))
}

func snapshotLockedNodes(def Definition) map[string]Node {
	out := make(map[string]Node)
	for _, node := range def.Nodes {
		if node.Locked {
			out[node.ID] = node
		}
	}

	return out
}

func reloadAndProtect(path string, locked map[string]Node) (Definition, error) {
	def, err := readDefinition(path)
	if err != nil {
		return Definition{}, err
	}

	changed := restoreLockedNodes(&def, locked)
	if err := def.Validate(); err != nil {
		return Definition{}, fmt.Errorf("validate loop file %q after edits: %w", path, err)
	}

	if changed {
		if err := writeDefinition(path, def); err != nil {
			return Definition{}, err
		}
	}

	return def, nil
}

func readDefinition(path string) (Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Definition{}, fmt.Errorf("read loop file %q: %w", path, err)
	}

	var def Definition
	def, err = decodeDefinition(data)
	if err != nil {
		return Definition{}, fmt.Errorf("parse loop file %q after edits: %w", path, err)
	}

	return def, nil
}

func restoreLockedNodes(def *Definition, locked map[string]Node) bool {
	if len(locked) == 0 {
		return false
	}

	changed := false
	for id, snapshot := range locked {
		index := slices.IndexFunc(def.Nodes, func(node Node) bool {
			return node.ID == id
		})
		if index == -1 {
			def.Nodes = append(def.Nodes, snapshot)
			changed = true

			continue
		}

		if !nodesEqual(def.Nodes[index], snapshot) {
			def.Nodes[index] = snapshot
			changed = true
		}
	}

	return changed
}

func nodesEqual(left Node, right Node) bool {
	return left.ID == right.ID &&
		left.Type == right.Type &&
		left.Locked == right.Locked &&
		left.Content == right.Content &&
		left.Settings.Model == right.Settings.Model &&
		left.Settings.MaxIterations == right.Settings.MaxIterations &&
		left.Settings.OnError == right.Settings.OnError
}

func writeDefinition(path string, def Definition) error {
	data, err := json.MarshalIndent(def, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal restored loop file %q: %w", path, err)
	}

	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("restore locked loop nodes in %q: %w", path, err)
	}

	return nil
}

func formatLoopSystemMessage(def Definition, path string, locked map[string]Node) string {
	lockedIDs := make([]string, 0, len(locked))
	unlockedIDs := make([]string, 0, len(def.Nodes)-len(locked))

	for _, node := range def.Nodes {
		if node.Locked {
			lockedIDs = append(lockedIDs, node.ID)
		} else {
			unlockedIDs = append(unlockedIDs, node.ID)
		}
	}

	var builder strings.Builder
	builder.WriteString("<agent_loop>\n")
	builder.WriteString("name: ")
	builder.WriteString(def.Name)
	builder.WriteString("\nfile: ")
	builder.WriteString(path)
	builder.WriteString("\nlocked_nodes: ")
	builder.WriteString(strings.Join(lockedIDs, ", "))
	builder.WriteString("\nunlocked_nodes: ")
	builder.WriteString(strings.Join(unlockedIDs, ", "))
	builder.WriteString("\n\nYou are running a user-defined agent loop. ")
	builder.WriteString("If improving unlocked loop nodes would improve the result, edit the loop JSON file directly. ")
	builder.WriteString("Do not modify locked nodes; nncode restores locked nodes after each loop step. ")
	builder.WriteString("Keep the loop file valid JSON and preserve the versioned v1 linear loop schema.\n")
	builder.WriteString("</agent_loop>")

	return builder.String()
}

func emitError(ctx context.Context, out chan<- agent.Event, err error) {
	emit(ctx, out, agent.Event{Type: agent.EventError, Err: err})
}

func emit(ctx context.Context, out chan<- agent.Event, ev agent.Event) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- ev:
		return true
	}
}
