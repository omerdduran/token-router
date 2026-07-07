package task

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Task struct {
	ID     string `json:"task_id"`
	Prompt string `json:"prompt"`
}

type Result struct {
	ID     string `json:"task_id"`
	Answer string `json:"answer"`
}

func Read(path string) ([]Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tasks: %w", err)
	}
	var tasks []Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		return nil, fmt.Errorf("parse tasks: %w", err)
	}
	return tasks, nil
}

// WriteAtomic writes results as valid JSON via a temp file + rename, so a
// crash mid-write can never leave a malformed results.json behind.
func WriteAtomic(path string, results []Result) error {
	if results == nil {
		results = []Result{}
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal results: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir output: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".results-*.json")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write results: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close results: %w", err)
	}
	return os.Rename(tmpName, path)
}
