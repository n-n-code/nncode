package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"nncode/internal/llm"
)

var errSessionIDRequired = errors.New("session ID or path is required")

// Session stores a conversation as a JSONL file.
type Session struct {
	ID       string
	FilePath string
	Messages []llm.Message
}

func New() *Session {
	id := strconv.FormatInt(time.Now().UnixNano(), 10)

	return &Session{
		ID:       id,
		Messages: make([]llm.Message, 0),
	}
}

// DefaultDir returns the directory used for session files.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot get user home directory: %w", err)
	}

	return filepath.Join(home, ".nncode", "sessions"), nil
}

// Resolve maps a session ID or file path to a JSONL session file path.
func Resolve(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errSessionIDRequired
	}

	if filepath.IsAbs(ref) || strings.ContainsRune(ref, filepath.Separator) {
		return ref, nil
	}

	if filepath.Ext(ref) == "" {
		ref += ".jsonl"
	}

	dir, err := DefaultDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, ref), nil
}

func (s *Session) AddMessage(msg llm.Message) {
	msg.Timestamp = time.Now().UnixMilli()
	s.Messages = append(s.Messages, msg)
}

// Save writes the session as JSONL. If dir is empty and FilePath is unset,
// defaults to ~/.nncode/sessions/.
func (s *Session) Save(dir string) error {
	if s.FilePath != "" {
		dir = filepath.Dir(s.FilePath)
	}

	if dir == "" {
		defaultDir, err := DefaultDir()
		if err != nil {
			return fmt.Errorf("cannot determine home directory: %w", err)
		}

		dir = defaultDir
	}

	err := os.MkdirAll(dir, 0755)
	if err != nil {
		return fmt.Errorf("cannot create session directory: %w", err)
	}

	if s.FilePath == "" {
		s.FilePath = filepath.Join(dir, s.ID+".jsonl")
	}

	f, err := os.Create(s.FilePath)
	if err != nil {
		return fmt.Errorf("cannot create session file: %w", err)
	}
	defer func() { _ = f.Close() }()

	writer := bufio.NewWriter(f)

	for _, msg := range s.Messages {
		line, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("cannot marshal message: %w", err)
		}

		_, err = writer.WriteString(string(line) + "\n")
		if err != nil {
			return fmt.Errorf("cannot write message: %w", err)
		}
	}

	err = writer.Flush()
	if err != nil {
		return fmt.Errorf("cannot flush session file: %w", err)
	}

	return nil
}

func Load(filePath string) (*Session, error) {
	sess := &Session{FilePath: filePath}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("cannot open session file: %w", err)
	}
	defer func() { _ = f.Close() }()

	base := filepath.Base(filePath)
	sess.ID = strings.TrimSuffix(base, filepath.Ext(base))
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var msg llm.Message
		err = json.Unmarshal([]byte(line), &msg)
		if err != nil {
			return nil, fmt.Errorf("cannot parse message: %w", err)
		}

		sess.Messages = append(sess.Messages, msg)
	}

	err = scanner.Err()
	if err != nil {
		return nil, fmt.Errorf("error reading session: %w", err)
	}

	return sess, nil
}

func List() ([]string, error) {
	dir, err := DefaultDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("cannot read sessions directory: %w", err)
	}

	var files []string

	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".jsonl" {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}

	sort.Strings(files)

	return files, nil
}
