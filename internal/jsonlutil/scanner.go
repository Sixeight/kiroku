package jsonlutil

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
)

// ForEachLine reads a JSONL file line by line, calling fn for each non-empty trimmed line.
// Returns an error if the file cannot be opened or if fn returns an error.
func ForEachLine(path string, fn func(line []byte) error) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}

		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			if fnErr := fn(line); fnErr != nil {
				return fnErr
			}
		}

		if errors.Is(err, io.EOF) {
			break
		}
	}

	return nil
}
