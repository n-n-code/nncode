// Package agentloop loads and runs user-defined prompt loops.
package agentloop

import (
	"errors"
	"fmt"
	"strings"
)

const (
	SchemaVersion        = 1
	DefaultMaxIterations = 10
)

type NodeType string

const (
	NodeEntryPrompt  NodeType = "entry_prompt"
	NodePrompt       NodeType = "prompt"
	NodeCmd          NodeType = "cmd"
	NodeExitCriteria NodeType = "exit_criteria"
	NodeExitPrompt   NodeType = "exit_prompt"
)

type OnError string

const (
	OnErrorAbort    OnError = "abort"
	OnErrorContinue OnError = "continue"
)

var (
	errLoopSchemaVersionRequired = errors.New("loop schema_version is required")
	errLoopSchemaVersionUnknown  = errors.New("loop schema_version is unsupported")
	errLoopNameRequired          = errors.New("loop name is required")
	errLoopNodesRequired         = errors.New("loop nodes are required")
	errLoopEntryRequired         = errors.New("loop requires exactly one entry_prompt node")
	errLoopExitCriteriaRequired  = errors.New("loop requires exactly one exit_criteria node")
	errLoopBodyRequired          = errors.New("loop requires at least one body node")
	errLoopExitPromptCount       = errors.New("loop can include at most one exit_prompt node")
	errLoopNodeIDRequired        = errors.New("loop node id is required")
	errLoopNodeIDDuplicate       = errors.New("loop node id is duplicated")
	errLoopNodeTypeUnknown       = errors.New("loop node type is unknown")
	errLoopNodeContentRequired   = errors.New("loop node content is required")
	errLoopMaxIterationsInvalid  = errors.New("loop max_iterations must not be negative")
	errNodeMaxIterationsInvalid  = errors.New("node settings cannot include max_iterations")
	errLoopSettingsOnError       = errors.New("loop settings cannot include on_error")
	errNodeOnErrorInvalid        = errors.New("node settings on_error is invalid")
	errNodeOnErrorUnsupported    = errors.New("node settings on_error is only supported on cmd nodes")
	errCmdNodeModelUnsupported   = errors.New("cmd node settings cannot include model")
)

type Definition struct {
	SchemaVersion int      `json:"schema_version"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Settings      Settings `json:"settings,omitzero"`
	Nodes         []Node   `json:"nodes"`
}

type Settings struct {
	Model         string  `json:"model,omitempty"`
	MaxIterations int     `json:"max_iterations,omitempty"`
	OnError       OnError `json:"on_error,omitempty"`
}

type Node struct {
	ID       string   `json:"id"`
	Type     NodeType `json:"type"`
	Locked   bool     `json:"locked,omitempty"`
	Settings Settings `json:"settings,omitzero"`
	Content  string   `json:"content"`
}

type nodeCounts struct {
	entry        int
	body         int
	exitCriteria int
	exitPrompt   int
}

func (d Definition) Validate() error {
	if d.SchemaVersion == 0 {
		return errLoopSchemaVersionRequired
	}

	if d.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: %d", errLoopSchemaVersionUnknown, d.SchemaVersion)
	}

	if strings.TrimSpace(d.Name) == "" {
		return errLoopNameRequired
	}

	if d.Settings.MaxIterations < 0 {
		return errLoopMaxIterationsInvalid
	}

	if d.Settings.OnError != "" {
		return errLoopSettingsOnError
	}

	if len(d.Nodes) == 0 {
		return errLoopNodesRequired
	}

	counts, err := validateNodes(d.Nodes)
	if err != nil {
		return err
	}

	return counts.validate()
}

func validateNodes(nodes []Node) (nodeCounts, error) {
	seen := make(map[string]struct{}, len(nodes))
	var counts nodeCounts

	for _, node := range nodes {
		if strings.TrimSpace(node.ID) == "" {
			return nodeCounts{}, errLoopNodeIDRequired
		}

		if _, ok := seen[node.ID]; ok {
			return nodeCounts{}, fmt.Errorf("%w: %q", errLoopNodeIDDuplicate, node.ID)
		}

		seen[node.ID] = struct{}{}

		if node.Settings.MaxIterations != 0 {
			return nodeCounts{}, fmt.Errorf("%w: %q", errNodeMaxIterationsInvalid, node.ID)
		}

		if err := validateNodeSettings(node); err != nil {
			return nodeCounts{}, err
		}

		next, err := countNode(node, counts)
		if err != nil {
			return nodeCounts{}, err
		}

		counts = next
	}

	return counts, nil
}

func countNode(node Node, counts nodeCounts) (nodeCounts, error) {
	switch node.Type {
	case NodeEntryPrompt:
		// entry_prompt content may be empty; the runner substitutes user input at invocation time.
		counts.entry++
	case NodePrompt:
		counts.body++
		if strings.TrimSpace(node.Content) == "" {
			return nodeCounts{}, fmt.Errorf("%w for prompt node %q", errLoopNodeContentRequired, node.ID)
		}
	case NodeCmd:
		counts.body++
		if strings.TrimSpace(node.Content) == "" {
			return nodeCounts{}, fmt.Errorf("%w for cmd node %q", errLoopNodeContentRequired, node.ID)
		}
	case NodeExitCriteria:
		counts.exitCriteria++
		if strings.TrimSpace(node.Content) == "" {
			return nodeCounts{}, fmt.Errorf("%w for exit_criteria node %q", errLoopNodeContentRequired, node.ID)
		}
	case NodeExitPrompt:
		counts.exitPrompt++
		if strings.TrimSpace(node.Content) == "" {
			return nodeCounts{}, fmt.Errorf("%w for exit_prompt node %q", errLoopNodeContentRequired, node.ID)
		}
	default:
		return nodeCounts{}, fmt.Errorf("%w: %q", errLoopNodeTypeUnknown, node.Type)
	}

	return counts, nil
}

func (c nodeCounts) validate() error {
	if c.entry != 1 {
		return errLoopEntryRequired
	}

	if c.body == 0 {
		return errLoopBodyRequired
	}

	if c.exitCriteria != 1 {
		return errLoopExitCriteriaRequired
	}

	if c.exitPrompt > 1 {
		return errLoopExitPromptCount
	}

	return nil
}

func validateNodeSettings(node Node) error {
	switch node.Settings.OnError {
	case "", OnErrorAbort, OnErrorContinue:
	default:
		return fmt.Errorf("%w for node %q: %q", errNodeOnErrorInvalid, node.ID, node.Settings.OnError)
	}

	if node.Type != NodeCmd && node.Settings.OnError != "" {
		return fmt.Errorf("%w: %q", errNodeOnErrorUnsupported, node.ID)
	}

	if node.Type == NodeCmd && strings.TrimSpace(node.Settings.Model) != "" {
		return fmt.Errorf("%w: %q", errCmdNodeModelUnsupported, node.ID)
	}

	return nil
}

func (d Definition) maxIterations() int {
	if d.Settings.MaxIterations > 0 {
		return d.Settings.MaxIterations
	}

	return DefaultMaxIterations
}

func (d Definition) entryNode() Node {
	for _, node := range d.Nodes {
		if node.Type == NodeEntryPrompt {
			return node
		}
	}

	return Node{} //nolint:exhaustruct // caller validates definitions first.
}

func (d Definition) bodyNodes() []Node {
	nodes := make([]Node, 0, len(d.Nodes))
	for _, node := range d.Nodes {
		if node.Type == NodePrompt || node.Type == NodeCmd {
			nodes = append(nodes, node)
		}
	}

	return nodes
}

func (d Definition) exitCriteriaNode() Node {
	for _, node := range d.Nodes {
		if node.Type == NodeExitCriteria {
			return node
		}
	}

	return Node{} //nolint:exhaustruct // caller validates definitions first.
}

func (d Definition) exitPromptNode() (Node, bool) {
	for _, node := range d.Nodes {
		if node.Type == NodeExitPrompt {
			return node, true
		}
	}

	return Node{}, false //nolint:exhaustruct // false reports absence.
}

func (n Node) onError() OnError {
	if n.Settings.OnError != "" {
		return n.Settings.OnError
	}

	return OnErrorAbort
}
