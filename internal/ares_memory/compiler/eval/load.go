package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// LoadSampleFile reads a single JSON sample file. When the file omits an "id",
// the base filename is used.
//
// The path is operator-supplied (an eval harness points at a directory of
// samples the operator controls), so reading it is by design rather than a
// tainted-input risk.
func LoadSampleFile(path string) (Sample, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-provided sample path
	if err != nil {
		return Sample{}, fmt.Errorf("eval: read %q: %w", path, err)
	}
	var s Sample
	if err := json.Unmarshal(data, &s); err != nil {
		return Sample{}, fmt.Errorf("eval: parse %q: %w", path, err)
	}
	if s.ID == "" {
		s.ID = filepath.Base(path)
	}
	return s, nil
}

// LoadDir reads every "*.json" file in dir (sorted by name) into a sample
// slice. Non-JSON files are ignored.
func LoadDir(dir string) ([]Sample, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("eval: read dir %q: %w", dir, err)
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) == ".json" {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(paths)
	samples := make([]Sample, 0, len(paths))
	for _, p := range paths {
		s, err := LoadSampleFile(p)
		if err != nil {
			return nil, err
		}
		samples = append(samples, s)
	}
	return samples, nil
}
