package sessionlog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
)

// TailMeta holds metadata extracted from the tail of a session file.
type TailMeta struct {
	Model        string
	ContextUsage *ContextUsage
}

// ContextUsage holds computed context usage data.
type ContextUsage struct {
	InputTokens   int `json:"input_tokens"`
	Percentage    int `json:"percentage"`
	ContextWindow int `json:"context_window"`
}

// tailChunkSize is how many bytes we read from the end of the file.
const tailChunkSize = 64 * 1024

// ExtractTailMeta reads the last portion of a session file to extract
// model and context usage without full DAG resolution. Returns nil (no
// error) if the file has no usable data.
func ExtractTailMeta(path string) (*TailMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // best-effort close on read-only file

	data, err := readTail(f, tailChunkSize)
	if err != nil {
		return nil, err
	}

	lines := splitLines(data)
	return extractFromLines(lines), nil
}

// readTail reads the last n bytes of r (or the whole thing if smaller).
func readTail(r io.ReadSeeker, n int64) ([]byte, error) {
	size, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	offset := size - n
	if offset < 0 {
		offset = 0
	}
	if _, err := r.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

// splitLines splits data into JSONL lines. Partial lines from a mid-file
// read are tolerated — they fail json.Unmarshal silently in the caller.
func splitLines(data []byte) [][]byte {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 256*1024), 256*1024)
	var lines [][]byte
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		cp := make([]byte, len(line))
		copy(cp, line)
		lines = append(lines, cp)
	}
	return lines
}

// tailEntry is the minimal structure we decode from each JSONL line.
type tailEntry struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message"`
}

// assistantMessage is the structure inside the "message" field for assistant entries.
type assistantMessage struct {
	Role  string `json:"role"`
	Model string `json:"model"`
	Usage *struct {
		InputTokens              int `json:"input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

// extractFromLines walks lines backwards to find model and context usage.
func extractFromLines(lines [][]byte) *TailMeta {
	var (
		model     string
		lastUsage *assistantMessage
	)

	// Walk backwards — we want the last entries.
	for i := len(lines) - 1; i >= 0; i-- {
		var entry tailEntry
		if err := json.Unmarshal(lines[i], &entry); err != nil {
			continue
		}

		// Check for assistant message with model/usage.
		if entry.Type == "assistant" && len(entry.Message) > 0 {
			var msg assistantMessage
			if err := json.Unmarshal(entry.Message, &msg); err != nil {
				continue
			}
			if model == "" && msg.Model != "" {
				model = msg.Model
			}
			if lastUsage == nil && msg.Usage != nil && msg.Usage.InputTokens > 0 {
				lastUsage = &msg
			}
		}

		// Once we have everything, stop scanning.
		if model != "" && lastUsage != nil {
			break
		}
	}

	if model == "" && lastUsage == nil {
		return nil
	}

	result := &TailMeta{Model: model}

	if lastUsage != nil && lastUsage.Usage != nil {
		effectiveModel := model
		if effectiveModel == "" && lastUsage.Model != "" {
			effectiveModel = lastUsage.Model
		}

		contextWindow := ModelContextWindow(effectiveModel)
		if contextWindow > 0 {
			totalInput := lastUsage.Usage.InputTokens +
				lastUsage.Usage.CacheReadInputTokens +
				lastUsage.Usage.CacheCreationInputTokens

			pct := totalInput * 100 / contextWindow
			if pct > 100 {
				pct = 100
			}

			result.ContextUsage = &ContextUsage{
				InputTokens:   totalInput,
				Percentage:    pct,
				ContextWindow: contextWindow,
			}
		}
	}

	return result
}
